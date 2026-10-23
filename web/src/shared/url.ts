export function isDisplayableHost(host: string): boolean {
  const value = host.trim().toLowerCase()
  if (!value) {
    return false
  }
  return !['0.0.0.0', '::', 'localhost', '127.0.0.1', '::1'].includes(value)
}

export function buildStudentURL(host: string, port: number | string): string {
  const value = host.trim()
  if (!isDisplayableHost(value)) {
    throw new Error(`invalid display host: ${host}`)
  }
  const portNumber = typeof port === 'string' ? Number.parseInt(port, 10) : port
  if (!Number.isInteger(portNumber) || portNumber < 1 || portNumber > 65535) {
    throw new Error(`invalid port: ${port}`)
  }
  const bracketed = value.includes(':') ? `[${value}]` : value
  return `http://${bracketed}:${portNumber}/`
}

export function portFromAddress(address: string): number | null {
  const match = /:(\d{1,5})$/.exec(address.trim())
  if (!match) {
    return null
  }
  const port = Number.parseInt(match[1], 10)
  return port >= 1 && port <= 65535 ? port : null
}
