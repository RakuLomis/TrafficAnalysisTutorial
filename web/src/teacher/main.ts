import { fetchConfig, fetchTeacherInfo, type PublicConfig } from '../shared/api'
import { qrDataURL } from '../shared/qr'
import { buildStudentURL, isDisplayableHost, portFromAddress } from '../shared/url'

function requireElement<T extends HTMLElement>(id: string): T {
  const element = document.getElementById(id)
  if (!element) {
    throw new Error(`missing element: ${id}`)
  }
  return element as T
}

const configStatus = requireElement<HTMLParagraphElement>('teacher-config-status')
const studentURL = requireElement<HTMLElement>('student-url')
const copyButton = requireElement<HTMLButtonElement>('copy-button')
const qrImage = requireElement<HTMLImageElement>('qr-image')
const qrPlaceholder = requireElement<HTMLParagraphElement>('qr-placeholder')
const ifaceSelect = requireElement<HTMLSelectElement>('iface-select')
const ifaceNote = requireElement<HTMLParagraphElement>('iface-note')
const protocolVersion = requireElement<HTMLElement>('protocol-version')
const roundStatus = requireElement<HTMLElement>('round-status')
const roundID = requireElement<HTMLElement>('round-id')

let currentURL = ''
let currentConfig: PublicConfig | null = null

function setError(message: string): void {
  configStatus.textContent = message
  configStatus.classList.add('error')
}

async function renderQR(url: string): Promise<void> {
  qrPlaceholder.hidden = false
  qrPlaceholder.textContent = '二维码生成中…'
  try {
    qrImage.src = await qrDataURL(url)
    qrImage.hidden = false
    qrPlaceholder.hidden = true
  } catch {
    qrImage.hidden = true
    qrPlaceholder.hidden = false
    qrPlaceholder.textContent = '二维码生成失败'
  }
}

async function applyURL(url: string): Promise<void> {
  currentURL = url
  studentURL.textContent = url
  copyButton.disabled = false
  await renderQR(url)
}

function portFor(cfg: PublicConfig): number {
  if (cfg.student_port && cfg.student_port > 0) {
    return cfg.student_port
  }
  const fromURL = cfg.student_url ? portFromAddress(new URL(cfg.student_url).host) : null
  return fromURL ?? 8080
}

function fillCandidates(cfg: PublicConfig, currentHost: string): void {
  const candidates = (cfg.iface_candidates ?? []).filter((item) => item.usable && isDisplayableHost(item.ip))
  if (candidates.length === 0) {
    ifaceSelect.innerHTML = '<option value="">未发现可用的局域网地址</option>'
    ifaceSelect.disabled = true
    ifaceNote.textContent = '请用 --iface-ip 指定地址后重启；不要把 0.0.0.0 或 localhost 给学生。'
    return
  }

  ifaceSelect.innerHTML = ''
  for (const item of candidates) {
    const option = document.createElement('option')
    option.value = item.ip
    option.textContent = `${item.ip} · ${item.name}${item.virtual ? '（虚拟）' : ''}`
    option.selected = item.ip === currentHost
    ifaceSelect.append(option)
  }
  ifaceSelect.disabled = false
  ifaceNote.textContent = '切换只改变本页二维码；持久选择请使用 --iface-ip 重启。'

  ifaceSelect.addEventListener('change', () => {
    if (!currentConfig) {
      return
    }
    try {
      void applyURL(buildStudentURL(ifaceSelect.value, portFor(currentConfig)))
    } catch (error) {
      setError(error instanceof Error ? error.message : '地址无效')
    }
  })
}

function currentHostFromURL(url: string): string {
  try {
    return new URL(url).hostname
  } catch {
    return ''
  }
}

async function copyURL(): Promise<void> {
  if (!currentURL) {
    return
  }
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(currentURL)
    } else {
      const area = document.createElement('textarea')
      area.value = currentURL
      area.style.position = 'fixed'
      area.style.opacity = '0'
      document.body.append(area)
      area.select()
      document.execCommand('copy')
      area.remove()
    }
    copyButton.textContent = '已复制'
    window.setTimeout(() => {
      copyButton.textContent = '复制'
    }, 1500)
  } catch {
    copyButton.textContent = '复制失败'
  }
}

async function load(): Promise<void> {
  let cfg: PublicConfig
  try {
    cfg = await fetchTeacherInfo()
  } catch {
    try {
      cfg = await fetchConfig()
    } catch (error) {
      setError(error instanceof Error ? error.message : '无法读取服务配置')
      return
    }
  }

  currentConfig = cfg
  configStatus.textContent = `服务已连接 · ${cfg.server_version ?? '开发版'}`
  protocolVersion.textContent = String(cfg.protocol_version)
  roundStatus.textContent = cfg.round.status
  roundID.textContent = cfg.round.id

  const host = cfg.student_url ? currentHostFromURL(cfg.student_url) : ''
  try {
    await applyURL(cfg.student_url ?? buildStudentURL(host || '0.0.0.0', portFor(cfg)))
  } catch (error) {
    setError(error instanceof Error ? error.message : '学生入口地址不可用')
  }
  fillCandidates(cfg, host)
}

copyButton.addEventListener('click', () => {
  void copyURL()
})

void load()
