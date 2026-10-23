import { checkCryptoCapabilities } from '../crypto/capabilities'

export function cryptoLibraryAvailable(): boolean {
  return checkCryptoCapabilities().box
}

export function secureRandomAvailable(): boolean {
  return checkCryptoCapabilities().secureRandom
}
