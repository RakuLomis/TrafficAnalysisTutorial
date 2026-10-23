import { fetchConfig, type PublicConfig } from '../shared/api'
import { cryptoLibraryAvailable, secureRandomAvailable } from '../shared/crypto'
import { isPongPayload } from '../shared/pong'

function requireElement<T extends HTMLElement>(id: string): T {
  const element = document.getElementById(id)
  if (!element) {
    throw new Error(`missing element: ${id}`)
  }
  return element as T
}

const configStatus = requireElement<HTMLParagraphElement>('config-status')
const cryptoStatus = requireElement<HTMLParagraphElement>('crypto-status')
const wsStatus = requireElement<HTMLParagraphElement>('ws-status')
const echoLog = requireElement<HTMLPreElement>('echo-log')
const echoButton = requireElement<HTMLButtonElement>('echo-button')

let socket: WebSocket | null = null

function appendLog(line: string): void {
  const stamp = new Date().toLocaleTimeString('zh-CN', { hour12: false })
  echoLog.textContent = `${echoLog.textContent ?? ''}[${stamp}] ${line}\n`
  echoLog.scrollTop = echoLog.scrollHeight
}

function renderConfig(cfg: PublicConfig): void {
  configStatus.textContent = `协议 v${cfg.protocol_version} · 课堂 ${cfg.round.status} · 轮次 ${cfg.round.id.slice(0, 8)}…`
  const encryption = cfg.capabilities.encryption && cryptoLibraryAvailable() && secureRandomAvailable()
  cryptoStatus.textContent = encryption
    ? `加密能力就绪：${cfg.limits.max_body_bytes} 字节正文上限`
    : '加密能力不可用：当前浏览器缺少安全随机数或密码库未加载'
  cryptoStatus.classList.toggle('error', !encryption)
}

async function loadConfig(): Promise<void> {
  try {
    renderConfig(await fetchConfig())
  } catch (error) {
    configStatus.textContent = error instanceof Error ? error.message : '课堂配置读取失败'
    configStatus.classList.add('error')
  }
}

function websocketURL(): string {
  const scheme = location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${scheme}//${location.host}/ws`
}

function testEcho(): void {
  if (socket) {
    socket.close()
    socket = null
  }
  echoButton.disabled = true
  wsStatus.textContent = '正在连接…'
  wsStatus.classList.remove('error')

  const target = websocketURL()
  const payload = JSON.stringify({ v: 1, type: 'ping' })
  const current = new WebSocket(target)
  socket = current
  const timeout = window.setTimeout(() => {
    if (current.readyState === WebSocket.OPEN) {
      current.close()
      wsStatus.textContent = '往返超时（5 秒）'
      wsStatus.classList.add('error')
    }
  }, 5000)

  current.addEventListener('open', () => {
    appendLog(`已连接 ${target}`)
    wsStatus.textContent = '已发送 ping，等待 pong…'
    current.send(payload)
  })

  current.addEventListener('message', (event) => {
    const text = typeof event.data === 'string' ? event.data : '<非文本消息>'
    appendLog(`收到消息：${text}`)
    if (isPongPayload(text)) {
      window.clearTimeout(timeout)
      wsStatus.textContent = 'WebSocket 往返成功'
      current.close()
    } else {
      wsStatus.textContent = '收到非预期响应'
      wsStatus.classList.add('error')
    }
  })

  current.addEventListener('error', () => {
    window.clearTimeout(timeout)
    wsStatus.textContent = 'WebSocket 连接失败'
    wsStatus.classList.add('error')
  })

  current.addEventListener('close', () => {
    window.clearTimeout(timeout)
    appendLog('连接已关闭')
    if (socket === current) {
      socket = null
    }
    echoButton.disabled = false
  })
}

echoButton.addEventListener('click', testEcho)

void loadConfig()
