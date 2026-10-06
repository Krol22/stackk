// The app's theme, remembered on this phone.

export const themes = [
  { id: 'auto', label: 'auto (phone setting)' },
  { id: 'dark', label: 'dark' },
  { id: 'light', label: 'light' },
  { id: 'gruvbox', label: 'gruvbox' },
  { id: 'nord', label: 'nord' },
  { id: 'tokyo', label: 'tokyo night' },
] as const

export type ThemeID = (typeof themes)[number]['id']

export function currentTheme(): ThemeID {
  const t = localStorage.getItem('stackk.theme')
  return themes.some((x) => x.id === t) ? (t as ThemeID) : 'auto'
}

export const sizes = [
  { id: 's', label: 'S', px: 13 },
  { id: 'm', label: 'M', px: 15 },
  { id: 'l', label: 'L', px: 17 },
] as const

export type SizeID = (typeof sizes)[number]['id']

export function currentSize(): SizeID {
  const s = localStorage.getItem('stackk.size')
  return sizes.some((x) => x.id === s) ? (s as SizeID) : 'm'
}

export function applySize(id: SizeID) {
  localStorage.setItem('stackk.size', id)
  document.documentElement.style.fontSize = `${sizes.find((x) => x.id === id)!.px}px`
}

export function applyTheme(id: ThemeID) {
  localStorage.setItem('stackk.theme', id)
  document.documentElement.dataset.theme = id
  // The status bar and the app switcher take this color.
  const bg = getComputedStyle(document.documentElement).getPropertyValue('--bg').trim()
  document.querySelector('meta[name="theme-color"]')?.setAttribute('content', bg)
}
