import DOMPurify from 'dompurify'

const tags = ['p', 'div', 'span', 'br', 'hr', 'strong', 'b', 'em', 'i', 'u', 's', 'del', 'blockquote', 'pre', 'code', 'ul', 'ol', 'li', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'table', 'thead', 'tbody', 'tfoot', 'tr', 'th', 'td', 'a']
const blocks = new Set(['P', 'DIV', 'HR', 'BLOCKQUOTE', 'PRE', 'UL', 'OL', 'LI', 'H1', 'H2', 'H3', 'H4', 'H5', 'H6', 'TABLE', 'THEAD', 'TBODY', 'TFOOT', 'TR'])

export function safeDescriptionURL(value, base) {
  try {
    const url = base ? new URL(value, base) : new URL(value)
    return ['http:', 'https:'].includes(url.protocol) && !url.username && !url.password ? url.href : ''
  } catch { return '' }
}

// The list uses text, while details use this same sanitized, basic-HTML allowlist.
// Images become text markers and explicit preview descriptors, never live <img>
// nodes. Merely opening a list/detail must not load remote images or beacons.
export function prepareDescription(value, sourceURL = '') {
  const raw = String(value ?? '')
  if (!/<\/?[a-z][a-z0-9-]*(?:\s[^<>]*|\/?)>/i.test(raw)) {
    const span = document.createElement('span')
    span.textContent = raw
    return { html: span.innerHTML, text: raw, plain: true, images: [] }
  }
  const fragment = DOMPurify.sanitize(raw, {
    ALLOWED_TAGS: [...tags, 'img'],
    ALLOWED_ATTR: ['href', 'title', 'colspan', 'rowspan', 'src', 'alt'],
    ALLOW_DATA_ATTR: false,
    ALLOW_ARIA_ATTR: false,
    RETURN_DOM_FRAGMENT: true,
  })
  const base = safeDescriptionURL(sourceURL)
  const images = []
  for (const img of fragment.querySelectorAll('img')) {
    const rawSource = img.getAttribute('src') || ''
    const url = rawSource.trim() ? safeDescriptionURL(rawSource, base) : ''
    const marker = document.createElement('span')
    if (url) {
      const label = (img.getAttribute('alt') || '').trim().slice(0, 80)
      images.push({ url, label })
      marker.textContent = `[图片 ${images.length}${label ? '：' + label : ''}]`
    } else marker.textContent = '[图片地址不可用]'
    img.replaceWith(marker)
  }
  // src/alt are only admitted during image extraction, not on rendered HTML.
  for (const node of fragment.querySelectorAll('[src],[alt]')) {
    node.removeAttribute('src'); node.removeAttribute('alt')
  }
  for (const link of fragment.querySelectorAll('a')) {
    const href = safeDescriptionURL(link.getAttribute('href') || '', base)
    if (!link.hasAttribute('href') || !href) link.removeAttribute('href')
    else {
      link.setAttribute('href', href)
      link.setAttribute('target', '_blank')
      link.setAttribute('rel', 'noopener noreferrer')
    }
  }
  const parts = []
  function read(node) {
    if (node.nodeType === Node.TEXT_NODE) { parts.push(node.textContent); return }
    const block = blocks.has(node.nodeName)
    if (block || node.nodeName === 'BR') parts.push('\n')
    for (const child of node.childNodes) read(child)
    if (block) parts.push('\n')
    if (['TD', 'TH'].includes(node.nodeName)) parts.push(' ')
  }
  read(fragment)
  const container = document.createElement('div')
  container.append(fragment)
  return { html: container.innerHTML, text: parts.join('').trim(), plain: false, images }
}

export function descriptionSummary(value, limit = 180, sourceURL = '') {
  const text = prepareDescription(value, sourceURL).text.replace(/\s+/g, ' ').trim()
  const characters = Array.from(text)
  return characters.length > limit ? characters.slice(0, limit).join('') + '…' : text
}
