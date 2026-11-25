function relativePath(value, directory = false) {
  return typeof value === 'string' && value.length > 0 && value.length <= 1024 && value.trim() === value &&
    !value.startsWith('/') && !/[:\\\0\r\n]/.test(value) && !value.split('/').some(part => part === '..' || part === '.git') &&
    (directory || !value.split('/').every(part => !part || part === '.'))
}
export function buildImageSourceError(config = {}) {
  if (config.mode !== 'container' || config.preset !== 'custom') return ''
  if (!config.image?.trim()) return '请填写自定义构建镜像和版本标签'
  if (!config.image_source || config.image_source === 'registry') return ''
  if (config.image_source !== 'dockerfile') return '请选择有效的构建镜像来源'
  if (config.image.includes('@')) return 'Dockerfile 构建镜像须填写版本标签，不能使用只读 digest 地址'
  if (!relativePath(config.dockerfile)) return '请填写源码目录内的 Dockerfile 相对路径'
  if (config.context && !relativePath(config.context, true)) return '构建上下文须为源码目录内的相对目录，默认 .'
  return ''
}
