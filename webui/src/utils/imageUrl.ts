// pixhost 图床缩略图相关工具函数。

// 将 pixhost 原图直链转换为缩略图直链，用于海报/截图页面的快速预览。
// 规则：https://img{N}.pixhost.cc/images/{ID}/{file} -> https://t{N}.pixhost.to/thumbs/{ID}/{file}
// 非 pixhost 原图直链（含 show/thumbs 页面、其他图床）不做转换，原样返回。
export function toPixhostThumbnail(originalUrl: string): string {
  if (!originalUrl) return originalUrl
  const match = /^https?:\/\/img(\d+)\.pixhost\.(?:to|cc)\/images\/(.+)$/i.exec(originalUrl.trim())
  if (!match) return originalUrl
  return `https://t${match[1]}.pixhost.to/thumbs/${match[2]}`
}

// 判断 URL 是否为 pixhost 原图直链（可转换为缩略图）。
export function isPixhostDirectImage(url: string): boolean {
  if (!url) return false
  return /^https?:\/\/img\d+\.pixhost\.(?:to|cc)\/images\/.+$/i.test(url.trim())
}
