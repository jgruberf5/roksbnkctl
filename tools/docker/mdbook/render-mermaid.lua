-- render-mermaid.lua — pandoc Lua filter that pre-renders Mermaid code
-- blocks to PNG via mermaid-cli (mmdc) so they embed as real diagrams in
-- the PDF output. Without this, pandoc would emit the literal mermaid
-- source as preformatted text in the PDF.
--
-- Wired into the mdbook-pandoc pipeline via book.toml's
-- [output.pandoc.profile.pdf]::filters setting. The HTML output path
-- doesn't use this filter; mdbook-mermaid + mermaid.min.js handle
-- client-side rendering for the browser.
--
-- Why PNG, not SVG (sprint 18 architect fix for the "shapes-but-no-text"
-- bug). mermaid-cli (v10+/v11) emits all in-node / on-edge labels via
-- `<foreignObject><div><span>…</span></div></foreignObject>` rather than
-- native SVG `<text>` elements. When pandoc / XeLaTeX feeds those SVGs
-- to librsvg (the SVG rasteriser bundled in the image), librsvg drops
-- `<foreignObject>` content entirely — the geometry survives but the
-- labels are blank. Rendering via mmdc's PNG output uses Puppeteer +
-- Chromium directly, which natively renders foreignObject + the
-- mermaid-default font stack, so the resulting raster has all text
-- intact. A retina-grade scale (-s 2) keeps the bitmap crisp on the
-- printed page; book.toml's `dpi` setting picks the on-page size.
--
-- PNG cache directory: /tmp/mermaid-png/ — keyed by SHA1 of the source,
-- so identical diagrams across chapters share the same rendered PNG.

local cache_dir = '/tmp/mermaid-png'
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
  local png_path = cache_dir .. '/' .. hash .. '.png'

  -- Reuse cached PNG when the source hash matches a previous render.
  local existing = io.open(png_path, 'r')
  if existing then
    existing:close()
    return png_path
  end

  local f, err = io.open(mmd_path, 'w')
  if not f then
    io.stderr:write('render-mermaid: cannot write ' .. mmd_path .. ': ' .. (err or '') .. '\n')
    return nil
  end
  f:write(source)
  f:close()

  -- mmdc flags:
  --   -b white    : opaque white background (PDF assumes white pages;
  --                 a transparent PNG renders muddy on XeLaTeX).
  --   -s 2        : 2x scale factor — keeps in-node labels legible at
  --                 the book's body-text size after XeLaTeX downscales
  --                 the bitmap to the column width.
  --   -p <config> : puppeteer config (system Chromium + --no-sandbox).
  local cmd = 'mmdc'
    .. ' -i ' .. shell_quote(mmd_path)
    .. ' -o ' .. shell_quote(png_path)
    .. ' -p ' .. shell_quote(puppeteer_config)
    .. ' -b white'
    .. ' -s 2'
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

  return png_path
end

function CodeBlock(elem)
  if not elem.classes:includes('mermaid') then
    return nil
  end

  local png_path = render_mermaid(elem.text)
  if not png_path then
    -- Render failed; leave the original CodeBlock in place so the reader
    -- sees the source instead of a missing diagram.
    return nil
  end

  -- Wrap the image in a centered Para. The empty caption keeps pandoc
  -- from generating a numbered figure caption (which would add a
  -- "Figure N:" prefix the prose around the diagram already covers).
  local img = pandoc.Image({}, png_path, '', { class = 'mermaid-rendered' })
  return pandoc.Para({ img })
end
