/* 主按钮的碳酸汽水填充，挂在 `.el-button--primary` 上
 * 设计取舍和调参见 docs/FRONTEND-NOTES.md 的「主按钮填充」一节 */

/** 液面的几个阶段，idle 静止、in 推进、settle 继续冒泡、done 收尾淡出、retreat 退潮 */
type Phase = 'idle' | 'in' | 'settle' | 'done' | 'retreat'

interface Bubble {
  x: number
  y: number
  vy: number
  vx: number
  r: number
  life: number
  age: number
  wobble: number
  tint: string
}

/** 贴着液面走的气泡，各有自己的相位和摆动 */
interface EdgeBubble {
  phase: number
  y: number
  wobble: number
  speed: number
  size: number
}

/** 一个按钮上的汽水状态和动画 */
class Soda {
  private readonly canvas: HTMLCanvasElement
  private readonly ctx: CanvasRenderingContext2D
  private readonly host: HTMLElement

  private W = 0
  private H = 0
  private raf = 0
  private last = 0

  private phase: Phase = 'idle'
  private progress = 0 // 0 到 1，液面推进
  private fadeOut = 1 // 1 到 0，收尾时气泡整体淡出
  private completed = false // 收尾完成，画布铺满后不再逐帧重绘
  private settleUntil = 0

  private palette = { w1: '', w2: '', w3: '', ink: '' }

  /* 边缘气泡贴着液面走，各有自己的相位和摆动 */
  private readonly edgeBubbles: EdgeBubble[] = Array.from({ length: 26 }, () => ({
    phase: Math.random(),
    y: Math.random(),
    wobble: Math.random() * Math.PI * 2,
    speed: 0.6 + Math.random() * 0.9,
    size: 1.0 + Math.random() * 2.2,
  }))

  /* 脱离液面的气泡，过期就原地删除，上限防止低配机器上越积越多 */
  private readonly trail: Bubble[] = []
  private static readonly MAX_TRAIL = 140

  /** 填充层本身，挂在按钮里、文字之下 */
  readonly element: HTMLSpanElement

  constructor(host: HTMLElement) {
    this.host = host
    this.canvas = document.createElement('canvas')
    const ctx = this.canvas.getContext('2d')
    if (!ctx) throw new Error('canvas 2d context unavailable')
    this.ctx = ctx

    this.element = document.createElement('span')
    this.element.className = 'soda-fill'
    this.element.setAttribute('aria-hidden', 'true')
    this.element.appendChild(this.canvas)
    host.prepend(this.element)

    host.addEventListener('pointerenter', this.onEnter)
    host.addEventListener('focus', this.onEnter)
    host.addEventListener('pointerleave', this.onLeave)
    host.addEventListener('blur', this.onLeave)
  }

  private onEnter = (): void => {
    if (this.reduceMotion) return
    this.readPalette()
    this.resize()
    this.phase = 'in'
    this.progress = 0
    this.fadeOut = 1
    this.completed = false
    this.trail.length = 0
    this.last = 0
    if (!this.raf) this.raf = requestAnimationFrame(this.loop)
  }

  private onLeave = (): void => {
    if (this.phase === 'idle' || this.progress <= 0) {
      this.reset()
      return
    }
    this.completed = false
    this.phase = 'retreat'
    if (!this.raf) this.raf = requestAnimationFrame(this.loop)
  }

  private get reduceMotion(): boolean {
    return typeof window !== 'undefined' && window.matchMedia
      ? window.matchMedia('(prefers-reduced-motion: reduce)').matches
      : false
  }

  private readPalette(): void {
    const cs = getComputedStyle(this.host)
    const pick = (name: string): string => cs.getPropertyValue(name).trim()
    this.palette = {
      w1: pick('--wave-1'),
      w2: pick('--wave-2'),
      w3: pick('--wave-3'),
      ink: pick('--text'),
    }
  }

  private resize(): void {
    const rect = this.host.getBoundingClientRect()
    const dpr = Math.min(window.devicePixelRatio || 1, 2)
    this.W = rect.width
    this.H = rect.height
    this.canvas.width = Math.round(this.W * dpr)
    this.canvas.height = Math.round(this.H * dpr)
    this.ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
  }

  /* 液面在某个高度上的 x 位置，三段不同波长的正弦叠加，各层错开相位 */
  private surfaceAt(t: number, yRatio: number, layer: number): number {
    const p = layer * 1.7
    const w =
      Math.sin(t * 2.1 + yRatio * 5.3 + p) * 1.8 +
      Math.sin(t * 3.7 - yRatio * 8.1 + 1.2 + p) * 1.1 +
      Math.sin(t * 5.9 + yRatio * 3.4 + 2.4 + p) * 0.6
    /* 过冲，覆盖波浪负半周缩回时右缘留下的缝 */
    const OVERSHOOT = 24
    const base = this.progress * (this.W + OVERSHOOT)
    return base + w * (0.6 + this.progress * 0.4)
  }

  private clamp01(v: number): number {
    return v < 0 ? 0 : v > 1 ? 1 : v
  }

  private draw(now: number): void {
    const t = now / 1000
    const { ctx, W, H } = this
    ctx.clearRect(0, 0, W, H)

    /* 收尾完成后整块铺满，跳过逐帧重绘。退潮除外，那时 progress 仍是 1 */
    if (this.completed && this.phase !== 'retreat') {
      ctx.fillStyle = this.palette.ink
      ctx.fillRect(0, 0, W, H)
      return
    }

    /* 三层灰带横向并排，从右往左画，后画的盖掉前一次的右段 */
    const SEG = 24
    const GAP = Math.max(18, W * 0.11)
    const bands = [
      { color: this.palette.w3, band: 2 }, // 浅灰，最前
      { color: this.palette.w2, band: 1 }, // 中灰
      { color: this.palette.ink, band: 0 }, // 墨色，最靠左
    ]

    /* 收尾时让后两层随推进收窄，否则它们的右缘会停在按钮里露出灰斑 */
    const catchUp = 1 - Math.pow(this.progress, 0.6)

    for (const band of bands) {
      // band 越大越靠前，偏移也越大，catchUp 到 0 时三层完全重合
      const offset = (band.band * GAP - GAP * 2) * catchUp
      ctx.beginPath()
      ctx.moveTo(-2, -2)
      for (let i = 0; i <= SEG; i++) {
        const yr = i / SEG
        const x = this.surfaceAt(t, yr, 0) + offset
        ctx.lineTo(x, yr * (H + 4) - 2)
      }
      ctx.lineTo(-2, H + 2)
      ctx.closePath()
      ctx.fillStyle = band.color
      ctx.fill()
    }

    // 边缘气泡贴着液面走，用两层灰交替
    for (const b of this.edgeBubbles) {
      const yr = b.y
      const x = this.surfaceAt(t, yr, 0) - b.phase * 26
      if (x < 4 || x > W) continue
      // 刚推进到的地方气泡最密，走过去了就淡
      const freshness = this.clamp01((x - (this.progress * W - 60)) / 40)
      const y = yr * H + Math.sin(t * 2 * b.speed + b.wobble) * 2.2
      ctx.globalAlpha = 0.9 * freshness
      ctx.fillStyle = b.size > 2.0 ? this.palette.w1 : this.palette.w3
      ctx.beginPath()
      ctx.arc(x, y, b.size, 0, Math.PI * 2)
      ctx.fill()
    }
    ctx.globalAlpha = 1

    // 尾缀气泡，从液面甩出后上浮、变小、消失
    for (let i = this.trail.length - 1; i >= 0; i--) {
      const b = this.trail[i]
      b.age += 1
      b.y -= b.vy
      b.x -= b.vx

      // 退潮时气泡被卷走，快速淡出，不走下面的右飘
      if (this.phase === 'retreat') {
        b.x -= 3.4
        b.y -= 0.4
        b.life -= 0.045
        if (b.life <= 0) {
          this.trail.splice(i, 1)
          continue
        }
        ctx.globalAlpha = Math.min(1, b.life * 1.6) * 0.8
        ctx.fillStyle = b.tint
        ctx.beginPath()
        ctx.arc(b.x, b.y, Math.max(0.2, b.r * b.life), 0, Math.PI * 2)
        ctx.fill()
        continue
      }

      // 收尾时向右上飘出，乘方让前半段几乎不动、后半段加速
      if (this.fadeOut < 1) {
        const pushed = Math.pow(1 - this.fadeOut, 2)
        b.x += pushed * 3.2
        b.y -= pushed * 1.1
      } else {
        // fadeOut 已归零但气泡还没飘出去，保持匀速右移直到越过右缘
        b.x += 2.4
        b.y -= 0.5
      }
      b.life -= 0.0075
      if (b.life <= 0) {
        this.trail.splice(i, 1)
        continue
      }

      // 不许越过波浪边界，每帧重取，因为液面本身在动
      const boundary = this.surfaceAt(t, b.y / H, 0)
      if (b.x + b.r > boundary) {
        b.x = boundary - b.r
      }
      // 落在负半周凹陷处就移除，避免卡在边界上闪烁
      if (b.x + b.r > boundary + 6) {
        this.trail.splice(i, 1)
        continue
      }
      if (b.x < -b.r) {
        this.trail.splice(i, 1)
        continue
      }

      const sway = Math.sin(b.age * 0.18 + b.wobble) * 0.8

      // 收尾淡出，fadeOut 从 1 线性降到 0
      const fade = this.fadeOut
      if (fade <= 0) {
        this.trail.splice(i, 1)
        continue
      }

      ctx.globalAlpha = Math.min(1, b.life * 1.6) * 0.8 * fade
      ctx.fillStyle = b.tint
      ctx.beginPath()
      ctx.arc(b.x + sway, b.y, Math.max(0.2, b.r * b.life * fade), 0, Math.PI * 2)
      ctx.fill()
    }
    ctx.globalAlpha = 1
  }

  private tints(): string[] {
    return [this.palette.w1, this.palette.w2, this.palette.w3]
  }

  /* 甩出一批气泡，六成从液面边缘出发，其余散布在已经推过的区域 */
  private spawnTrail(count: number): void {
    const tints = this.tints()
    for (let i = 0; i < count && this.trail.length < Soda.MAX_TRAIL; i++) {
      const fromEdge = Math.random() < 0.65
      const yr = Math.random()
      const edgeX = this.surfaceAt(performance.now() / 1000, yr, 0)
      this.trail.push({
        x: fromEdge ? edgeX - Math.random() * 16 : Math.random() * Math.max(1, edgeX),
        y: yr * this.H,
        vy: 0.3 + Math.random() * 0.55,
        vx: -0.2 + Math.random() * 0.4,
        r: 0.9 + Math.random() * 2.1,
        life: 1,
        age: 0,
        wobble: Math.random() * Math.PI * 2,
        tint: tints[(Math.random() * tints.length) | 0],
      })
    }
  }

  private loop = (now: number): void => {
    const dt = this.last ? Math.min(now - this.last, 64) : 16
    this.last = now

    if (this.phase === 'in') {
      // 前快后慢，开头立刻响应操作，末尾缓下来
      const v = 1.9 * (1 - this.progress * 0.72)
      this.progress += (dt / 1000) * v
      if (this.progress >= 1) {
        this.progress = 1
        this.phase = 'settle'
        this.settleUntil = now + 420 // 液面到位后再吐一会儿气泡
      }
      if (Math.random() < 0.9) this.spawnTrail(1 + (Math.random() < 0.6 ? 1 : 0))
    } else if (this.phase === 'settle') {
      // 液面已经铺满，继续冒泡让残余气泡铺开
      if (now < this.settleUntil && Math.random() < 0.7) {
        this.spawnTrail(1 + (Math.random() < 0.4 ? 1 : 0))
      } else if (now >= this.settleUntil) {
        this.phase = 'done'
      }
    } else if (this.phase === 'retreat') {
      // 退潮，液面快速缩回左侧，缓入让水流像被抽走而不是被切掉
      const t0 = 1 - this.progress
      this.progress -= (dt / 1000) * (1.2 + t0 * 2.4)
      if (this.progress <= 0) {
        // 退干净了，复位并跳过本帧绘制
        this.progress = 0
        this.trail.length = 0
        this.reset()
        return
      }
    } else if (this.phase === 'done') {
      // 停止冒泡后让剩余气泡淡出，约 0.9s
      this.fadeOut -= dt / 900
      if (this.fadeOut <= 0) {
        this.fadeOut = 0
        // 等气泡自己飘出去，不能提前铺满画布，否则会把它们盖住造成跳变
        if (this.trail.length === 0) this.completed = true
      }
    }

    this.draw(now)

    // 只要还有气泡在飘就继续跑，completed 之后画布已是纯墨色，不再逐帧重绘
    if (!this.completed && this.phase !== 'idle') {
      this.raf = requestAnimationFrame(this.loop)
    } else {
      this.raf = 0
      this.last = 0
    }
  }

  /** 立即复位到初始状态，供 onLeave 和退潮结束后调用 */
  private reset(): void {
    if (this.raf) cancelAnimationFrame(this.raf)
    this.raf = 0
    this.last = 0
    this.phase = 'idle'
    this.progress = 0
    this.fadeOut = 1
    this.completed = false
    this.trail.length = 0
    this.ctx.clearRect(0, 0, this.W, this.H)
  }

  destroy(): void {
    this.reset()
    this.host.removeEventListener('pointerenter', this.onEnter)
    this.host.removeEventListener('focus', this.onEnter)
    this.host.removeEventListener('pointerleave', this.onLeave)
    this.host.removeEventListener('blur', this.onLeave)
    this.element.remove()
  }
}

/* 全局委托。主按钮散落在多个视图里，异步渲染和对话框里的按钮运行时才进 DOM，
   逐个挂指令容易漏，这里用 MutationObserver 统一接管 */

const attached = new WeakMap<HTMLElement, Soda>()

/** 给一个主按钮挂上汽水填充。重复调用是空操作 */
export function attachSoda(button: HTMLElement): void {
  if (attached.has(button)) return
  attached.set(button, new Soda(button))
}

const SELECTOR = '.el-button--primary'

function scan(root: ParentNode): void {
  if (root instanceof HTMLElement && root.matches(SELECTOR)) attachSoda(root)
  for (const el of root.querySelectorAll<HTMLElement>(SELECTOR)) attachSoda(el)
}

let installed = false

/** 安装一次，之后整个页面生命周期内自动接管新增的主按钮 */
export function installSodaFill(): void {
  if (installed || typeof document === 'undefined') return
  installed = true

  scan(document)

  const observer = new MutationObserver((records) => {
    for (const record of records) {
      for (const node of record.addedNodes) {
        if (node instanceof HTMLElement) scan(node)
      }
    }
  })
  observer.observe(document.body, { childList: true, subtree: true })
}
