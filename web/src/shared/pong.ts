export function isPongPayload(payload: string): boolean {
  try {
    const value: unknown = JSON.parse(payload)
    if (typeof value !== 'object' || value === null) {
      return false
    }
    const record = value as Record<string, unknown>
    return record.v === 1 && record.type === 'pong'
  } catch {
    return false
  }
}
