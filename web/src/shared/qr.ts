import QRCode from 'qrcode'

export async function qrDataURL(text: string): Promise<string> {
  return QRCode.toDataURL(text, {
    errorCorrectionLevel: 'M',
    margin: 1,
    width: 320,
    color: {
      dark: '#0f172a',
      light: '#ffffff',
    },
  })
}
