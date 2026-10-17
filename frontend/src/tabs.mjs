const titles = {
  '/overview': '运行概览',
  '/processes': '进程管理',
  '/configuration': '配置管理',
  '/settings': '安全设置'
}
const home = { key: '/overview', title: '运行概览', url: '/overview' }
export function tabForRoute(route) {
  if (titles[route.path]) return { key: route.path, title: titles[route.path], url: route.path }
  if (
    route.path === '/logs' &&
    typeof route.query?.name === 'string' &&
    route.query.name.length <= 256
  ) {
    const node =
      typeof route.query.node === 'string' && route.query.node.length <= 256 ? route.query.node : ''
    const query = new URLSearchParams({
      node,
      name: route.query.name,
      stream: route.query.stream === 'stderr' ? 'stderr' : 'stdout'
    })
    return {
      key: `/logs:${node}:${route.query.name}`,
      title: `${route.query.name} · 日志`,
      url: `/logs?${query}`
    }
  }
  return null
}
export function restoreTabs(raw) {
  try {
    const values = JSON.parse(raw)
    if (!Array.isArray(values)) return [{ ...home }]
    const tabs = [{ ...home }]
    for (const value of values.slice(0, 16)) {
      if (typeof value?.url !== 'string' || !value.url.startsWith('/')) continue
      const parsed = new URL(value.url, 'https://supervisord.invalid')
      if (parsed.origin !== 'https://supervisord.invalid') continue
      const tab = tabForRoute({
        path: parsed.pathname,
        query: Object.fromEntries(parsed.searchParams)
      })
      if (tab && !tabs.some((item) => item.key === tab.key)) tabs.push(tab)
    }
    return tabs.slice(0, 16)
  } catch {
    return [{ ...home }]
  }
}
export function appendLog(current, incoming, reset, limit = 256 * 1024) {
  const text = reset ? incoming : current + incoming
  const bounded = text.slice(-limit)
  const lines = bounded.split('\n')
  return lines.length > 2000 ? lines.slice(-2000).join('\n') : bounded
}
