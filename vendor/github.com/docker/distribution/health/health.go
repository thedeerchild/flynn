package health

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/docker/distribution/context"
)

var (
	mutex            sync.RWMutex
	registeredChecks = make(map[string]Checker)
)

// Checker is the interface for a Health Checker
type Checker interface {
	// Check returns nil if the service is okay.
	Check() error
}

// CheckFunc is a convenience type to create functions that implement
// the Checker interface
type CheckFunc func() error

// Check Implements the Checker interface to allow for any func() error method
// to be passed as a Checker
func (cf CheckFunc) Check() error {
	return cf()
}

// Updater implements a health check that is explicitly set.
type Updater interface {
	Checker

	// Update updates the current status of the health check.
	Update(status error)
}

// updater implements Checker and Updater, providing an asynchronous Update
// method.
// This allows us to have a Checker that returns the Check() call immediately
// not blocking on a potentially expensive check.
type updater struct {
	mu     sync.Mutex
	status error
}

// Check implements the Checker interface
func (u *updater) Check() error {
	u.mu.Lock()
	defer u.mu.Unlock()

	return u.status
}

// Update implements the Updater interface, allowing asynchronous access to
// the status of a Checker.
func (u *updater) Update(status error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	u.status = status
}

// NewStatusUpdater returns a new updater
func NewStatusUpdater() Updater {
	return &updater{}
}

// thresholdUpdater implements Checker and Updater, providing an asynchronous Update
// method.
// This allows us to have a Checker that returns the Check() call immediately
// not blocking on a potentially expensive check.
type thresholdUpdater struct {
	mu        sync.Mutex
	status    error
	threshold int
	count     int
}

// Check implements the Checker interface
func (tu *thresholdUpdater) Check() error {
	tu.mu.Lock()
	defer tu.mu.Unlock()

	if tu.count >= tu.threshold {
		return tu.status
	}

	return nil
}

// thresholdUpdater implements the Updater interface, allowing asynchronous
// access to the status of a Checker.
func (tu *thresholdUpdater) Update(status error) {
	tu.mu.Lock()
	defer tu.mu.Unlock()

	if status == nil {
		tu.count = 0
	} else if tu.count < tu.threshold {
		tu.count++
	}

	tu.status = status
}

// NewThresholdStatusUpdater returns a new thresholdUpdater
func NewThresholdStatusUpdater(t int) Updater {
	return &thresholdUpdater{threshold: t}
}

// PeriodicChecker wraps an updater to provide a periodic checker
func PeriodicChecker(check Checker, period time.Duration) Checker {
	u := NewStatusUpdater()
	go func() {
		t := time.NewTicker(period)
		for {
			<-t.C
			u.Update(check.Check())
		}
	}()

	return u
}

// PeriodicThresholdChecker wraps an updater to provide a periodic checker that
// uses a threshold before it changes status
func PeriodicThresholdChecker(check Checker, period time.Duration, threshold int) Checker {
	tu := NewThresholdStatusUpdater(threshold)
	go func() {
		t := time.NewTicker(period)
		for {
			<-t.C
			tu.Update(check.Check())
		}
	}()

	return tu
}

// CheckStatus returns a map with all the current health check errors
func CheckStatus() map[string]string { // TODO(stevvooe) this needs a proper type
	mutex.RLock()
	defer mutex.RUnlock()
	statusKeys := make(map[string]string)
	for k, v := range registeredChecks {
		err := v.Check()
		if err != nil {
			statusKeys[k] = err.Error()
		}
	}

	return statusKeys
}

// Register associates the checker with the provided name. We allow
// overwrites to a specific check status.
func Register(name string, check Checker) {
	mutex.Lock()
	defer mutex.Unlock()
	_, ok := registeredChecks[name]
	if ok {
		panic("Check already exists: " + name)
	}
	registeredChecks[name] = check
}

// RegisterFunc allows the convenience of registering a checker directly
// from an arbitrary func() error
func RegisterFunc(name string, check func() error) {
	Register(name, CheckFunc(check))
}

// RegisterPeriodicFunc allows the convenience of registering a PeriodicChecker
// from an arbitrary func() error
func RegisterPeriodicFunc(name string, period time.Duration, check CheckFunc) {
	Register(name, PeriodicChecker(CheckFunc(check), period))
}

// RegisterPeriodicThresholdFunc allows the convenience of registering a
// PeriodicChecker from an arbitrary func() error
func RegisterPeriodicThresholdFunc(name string, period time.Duration, threshold int, check CheckFunc) {
	Register(name, PeriodicThresholdChecker(CheckFunc(check), period, threshold))
}

// StatusHandler returns a JSON blob with all the currently registered Health Checks
// and their corresponding status.
// Returns 503 if any Error status exists, 200 otherwise
func StatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		checks := CheckStatus()
		status := http.StatusOK

		// If there is an error, return 503
		if len(checks) != 0 {
			status = http.StatusServiceUnavailable
		}

		statusResponse(w, r, status, checks)
	} else {
		http.NotFound(w, r)
	}
}

// Handler returns a handler that will return 503 response code if the health
// checks have failed. If everything is okay with the health checks, the
// handler will pass through to the provided handler. Use this handler to
// disable a web application when the health checks fail.
func Handler(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checks := CheckStatus()
		if len(checks) != 0 {
			statusResponse(w, r, http.StatusServiceUnavailable, checks)
			return
		}

		handler.ServeHTTP(w, r) // pass through
	})
}

// statusResponse completes the request with a response describing the health
// of the service.
func statusResponse(w http.ResponseWriter, r *http.Request, status int, checks map[string]string) {
	p, err := json.Marshal(checks)
	if err != nil {
		context.GetLogger(context.Background()).Errorf("error serializing health status: %v", err)
		p, err = json.Marshal(struct {
			ServerError string `json:"server_error"`
		}{
			ServerError: "Could not parse error message",
		})
		status = http.StatusInternalServerError

		if err != nil {
			context.GetLogger(context.Background()).Errorf("error serializing health status failure message: %v", err)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", fmt.Sprint(len(p)))
	w.WriteHeader(status)
	if _, err := w.Write(p); err != nil {
		context.GetLogger(context.Background()).Errorf("error writing health status response body: %v", err)
	}
}

// Registers global /debug/health api endpoint
func init() {
	http.HandleFunc("/debug/health", StatusHandler)
}
