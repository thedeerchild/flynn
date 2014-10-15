require 'pygments.rb'
require 'cgi'
require 'middleman-core/renderers/redcarpet'

REDCARPET_EXTENTIONS = {
  fenced_code_blocks: true,
  no_intra_emphasis: true,
  autolink: true,
  tables: true,
  strikethrough: true,
  lax_html_blocks: true,
  space_after_headers: true,
  superscript: true,
  #with_toc_data: true
}.freeze

module MarkdownHelpers
  def anchor(text)
    if m = text.match(/\A<a[^>]+>([^<]+)<\/a>/)
      text = m[1]
    end

    text.downcase.strip.gsub(/<\/?[^>]*>`/, '').gsub(/\s+/, '-')
  end

  def el(el, content, attributes = {})
    if content
      attrs = attributes ? ' ' + attributes.map { |k,v| "#{k}=\"#{v}\"" }.join(' ') : ''
      "<#{el}#{attrs}>\n#{content}</#{el}>\n"
    else
      ''
    end
  end
end

class Middleman::Renderers::MiddlemanRedcarpetHTML
  include Redcarpet::Render::SmartyPants
  include MarkdownHelpers

  def block_code(code, language)
    language == "text" ? el('pre', CGI::escapeHTML(code)) : Pygments.highlight(code, lexer: language)
  end

  def header(text, level)
    el("h#{level}", text, id: anchor(text))
  end
end
