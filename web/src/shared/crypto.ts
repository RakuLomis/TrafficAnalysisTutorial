import nacl from 'tweetnacl'

export function cryptoLibraryAvailable(): boolean {
  return typeof nacl.box?.keyPair === 'function' && typeof nacl.box?.before === 'function'
}

export function secureRandomAvailable(): boolean {
  return typeof globalThis.crypto?.getRandomValues === 'function'
}
