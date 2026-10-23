import nacl from 'tweetnacl'

export interface CryptoCapabilities {
  secureRandom: boolean
  box: boolean
  encryption: boolean
}

export function checkCryptoCapabilities(): CryptoCapabilities {
  const secureRandom = typeof globalThis.crypto?.getRandomValues === 'function'
  const box = typeof nacl.box?.keyPair === 'function' && typeof nacl.box?.before === 'function'
  return {
    secureRandom,
    box,
    encryption: secureRandom && box,
  }
}

export function assertCryptoAvailable(): void {
  const capabilities = checkCryptoCapabilities()
  if (!capabilities.encryption) {
    throw new Error('encryption unavailable: secure random or nacl.box is missing')
  }
}
