import DOMPurify from 'dompurify'
import { marked } from 'marked'

marked.setOptions({ gfm: true, breaks: true })

// Links open outside the app; nothing an agent writes may run script or load from elsewhere.
DOMPurify.addHook('afterSanitizeAttributes', (node) => {
  if (node.tagName === 'A') {
    node.setAttribute('target', '_blank')
    node.setAttribute('rel', 'noopener noreferrer')
  }
})

// md renders an agent's markdown to sanitized HTML: its text comes from repos and web pages
// you don't control.
export function md(text: string): string {
  const html = marked.parse(text, { async: false })
  return DOMPurify.sanitize(html, {
    FORBID_TAGS: ['img', 'style', 'iframe', 'form', 'input', 'video', 'audio', 'svg', 'math'],
    FORBID_ATTR: ['style'],
  })
}
