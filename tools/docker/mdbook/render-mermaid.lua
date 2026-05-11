-- render-mermaid.lua — pandoc Lua filter that pre-renders Mermaid code
-- blocks to SVG via mermaid-cli (mmdc) so they embed as real diagrams in
-- the PDF output. Without this, pandoc would emit the literal mermaid
-- source as preformatted text in the PDF.
--
-- Wired into the mdbook-pandoc pipeline via book.toml's
-- [output.pandoc.profile.pdf]::lua-filter setting. The HTML output path
-- doesn't use this filter; mdbook-mermaid + mermaid.min.js handle
-- client-side rendering for the browser.
--
-- SVG cache directory: /tmp/mermaid-svg/ — keyed by SHA1 of the source,
-- so identical diagrams across chapters share the same rendered SVG.

local cache_dir = '/tmp/mermaid-svg'
os.execute('mkdir -p ' .. cache_dir)

local puppeteer_config = os.getenv('MERMAID_PUPPETEER_CONFIG')
    or '/opt/puppeteer-config.json'

local function shell_quote(s)
  return "'" .. s:gsub("'", "'\\''") .. "'"
end

-- mdbook's HTML renderer entity-encodes angle brackets, quotes, and
-- ampersands inside mermaid code blocks (so `<ws>` becomes `&lt;ws&gt;`
-- by the time mdbook-pandoc forwards the block to pandoc). mmdc's
-- parser doesn't understand HTML entities and bails on `&lt;`. Decode
-- the common entities back to their literal characters before handing
-- to mmdc.
local function decode_html_entities(s)
  return s
    :gsub('&lt;',   '<')
    :gsub('&gt;',   '>')
    :gsub('&quot;', '"')
    :gsub('&apos;', "'")
    :gsub('&amp;',  '&')
end

local function render_mermaid(source)
  source = decode_html_entities(source)
  local hash = pandoc.utils.sha1(source)
  local mmd_path = cache_dir .. '/' .. hash .. '.mmd'
  local svg_path = cache_dir .. '/' .. hash .. '.svg'

  -- Reuse cached SVG when the source hash matches a previous render.
  local existing = io.open(svg_path, 'r')
  if existing then
    existing:close()
    return svg_path
  end

  local f, err = io.open(mmd_path, 'w')
  if not f then
    io.stderr:write('render-mermaid: cannot write ' .. mmd_path .. ': ' .. (err or '') .. '\n')
    return nil
  end
  f:write(source)
  f:close()

  local cmd = 'mmdc'
    .. ' -i ' .. shell_quote(mmd_path)
    .. ' -o ' .. shell_quote(svg_path)
    .. ' -p ' .. shell_quote(puppeteer_config)
    .. ' --quiet'
    .. ' 2>&1'

  local pipe = io.popen(cmd, 'r')
  if not pipe then
    io.stderr:write('render-mermaid: io.popen failed for: ' .. cmd .. '\n')
    return nil
  end
  local output = pipe:read('*a')
  local ok, _, code = pipe:close()
  if not ok then
    io.stderr:write('render-mermaid: mmdc exited ' .. tostring(code) .. ' on ' .. mmd_path .. '\n')
    io.stderr:write(output)
    return nil
  end

  return svg_path
end

function CodeBlock(elem)
  if not elem.classes:includes('mermaid') then
    return nil
  end

  local svg_path = render_mermaid(elem.text)
  if not svg_path then
    -- Render failed; leave the original CodeBlock in place so the reader
    -- sees the source instead of a missing diagram.
    return nil
  end

  -- Wrap the image in a centered Para. The empty caption keeps pandoc
  -- from generating a numbered figure caption (which would add a
  -- "Figure N:" prefix the prose around the diagram already covers).
  local img = pandoc.Image({}, svg_path, '', { class = 'mermaid-rendered' })
  return pandoc.Para({ img })
end
