import * as THREE from 'three'
import { GLTFLoader } from 'three/examples/jsm/loaders/GLTFLoader.js'
import { OrbitControls } from 'three/examples/jsm/controls/OrbitControls.js'
import { EffectManager } from './effects'
import { Fx } from './fx'
import type { GameEvent, Snapshot, SkillInfo } from '../types'

export const TEAM_COLORS = ['#35e0ff', '#ff4d6d']
const GK_COLORS = ['#b6fff0', '#ffc27a']

interface PlayerObj {
  root: THREE.Group
  body: THREE.Object3D
  target: THREE.Vector3
  yaw: number
  label: HTMLDivElement
  nm: HTMLSpanElement
  castEl: HTMLDivElement
  castTimer?: ReturnType<typeof setTimeout>
  bubble: HTMLDivElement
  ice: THREE.Mesh
  shield: THREE.Mesh
  compile: THREE.Mesh
  ring: THREE.Mesh
  team: number
  controlled: boolean
  hasted?: boolean
}

const loader = new GLTFLoader()
const loadGlb = (url: string) => loader.loadAsync(url).then((g) => g.scene).catch(() => null)

export class Arena {
  readonly scene = new THREE.Scene()
  private camera = new THREE.PerspectiveCamera(42, 1, 0.5, 400)
  private renderer: THREE.WebGLRenderer
  private controls: OrbitControls
  private effects = new EffectManager(this.scene)
  private fx!: Fx
  private trailColor = '#ffffff'
  private trailUntil = 0
  private ghostClock = 0
  private slowRings = new Map<string, THREE.Mesh>()
  private players = new Map<string, PlayerObj>()
  private projs = new Map<number, THREE.Mesh>()
  private walls = new Map<number, THREE.Mesh>()
  private skills = new Map<string, SkillInfo>()
  private ball = new THREE.Group()
  private ballTarget = new THREE.Vector3(0, 1, 0)
  private shadow = new THREE.Mesh(new THREE.CircleGeometry(1, 20), new THREE.MeshBasicMaterial({ color: '#000000', transparent: true, opacity: 0.35, depthWrite: false }))
  private templates: { player: THREE.Object3D | null; ball: THREE.Object3D | null } = { player: null, ball: null }
  private pendingSnap: Snapshot | null = null
  private shake = 0
  private raf = 0
  private last = performance.now()
  private ro: ResizeObserver
  private disposed = false
  private userMoved = false

  constructor(private canvas: HTMLCanvasElement, private overlay: HTMLElement) {
    this.renderer = new THREE.WebGLRenderer({ canvas, antialias: true })
    this.renderer.setPixelRatio(Math.min(devicePixelRatio, 2))
    this.renderer.shadowMap.enabled = true
    this.scene.background = new THREE.Color('#070b14')
    this.scene.fog = new THREE.Fog('#070b14', 120, 220)
    this.camera.position.set(0, 52, 62)
    this.controls = new OrbitControls(this.camera, canvas)
    this.controls.target.set(0, 0, 4)
    this.controls.maxPolarAngle = Math.PI * 0.47
    this.controls.minDistance = 25
    this.controls.maxDistance = 150
    this.controls.enableDamping = true
    this.controls.addEventListener('start', () => (this.userMoved = true))

    this.scene.add(new THREE.HemisphereLight('#bcd4ff', '#10301a', 1.0))
    const sun = new THREE.DirectionalLight('#fff4e0', 2.2)
    sun.position.set(-30, 70, 40)
    sun.castShadow = true
    sun.shadow.mapSize.set(2048, 2048)
    Object.assign(sun.shadow.camera, { left: -70, right: 70, top: 45, bottom: -45, near: 10, far: 200 })
    this.scene.add(sun)

    this.ball.position.copy(this.ballTarget)
    this.scene.add(this.ball)
    this.shadow.rotation.x = -Math.PI / 2
    this.shadow.position.y = 0.04
    this.scene.add(this.shadow)

    this.fx = new Fx(this.scene, overlay)
    this.ro = new ResizeObserver(() => this.resize())
    this.ro.observe(canvas.parentElement ?? canvas)
    this.resize()
    this.loadAssets()
    this.raf = requestAnimationFrame(this.frame)
  }

  private async loadAssets() {
    const [pitch, player, ball] = await Promise.all([
      loadGlb('/models/pitch.glb'), loadGlb('/models/player.glb'), loadGlb('/models/ball.glb'),
    ])
    if (this.disposed) return
    this.scene.add(pitch ?? this.fallbackPitch())
    pitch?.traverse((o) => { o.receiveShadow = true })
    this.templates.player = player
    this.templates.ball = ball
    this.ball.clear()
    this.ball.add(ball ? ball.clone(true) : new THREE.Mesh(new THREE.IcosahedronGeometry(1, 1), new THREE.MeshStandardMaterial({ color: '#f4f4f4', flatShading: true })))
    this.ball.traverse((o) => { o.castShadow = true })
    // rebuild players created before assets arrived
    for (const [id, p] of this.players) { this.scene.remove(p.root); this.dropPlayer(id, p) }
    if (this.pendingSnap) this.update({ ...this.pendingSnap, events: [] })
  }

  private fallbackPitch() {
    const g = new THREE.Group()
    const c = document.createElement('canvas'); c.width = 1024; c.height = 614
    const x = c.getContext('2d')!
    for (let i = 0; i < 10; i++) { x.fillStyle = i % 2 ? '#2f8f3c' : '#37a046'; x.fillRect(i * 102.4, 0, 103, 614) }
    x.strokeStyle = '#fff'; x.lineWidth = 3; x.strokeRect(10, 10, 1004, 594)
    x.beginPath(); x.moveTo(512, 10); x.lineTo(512, 604); x.stroke()
    x.beginPath(); x.arc(512, 307, 90, 0, 7); x.stroke()
    const m = new THREE.Mesh(new THREE.PlaneGeometry(100, 60), new THREE.MeshStandardMaterial({ map: new THREE.CanvasTexture(c) }))
    m.rotation.x = -Math.PI / 2; m.receiveShadow = true
    g.add(m)
    for (const sx of [-1, 1]) {
      const goal = new THREE.Mesh(new THREE.BoxGeometry(4, 3, 14), new THREE.MeshBasicMaterial({ color: '#ffffff', wireframe: true }))
      goal.position.set(sx * 52, 1.5, 0); g.add(goal)
    }
    return g
  }

  private makePlayer(id: string, team: number, role: string): PlayerObj {
    const root = new THREE.Group()
    let body: THREE.Object3D
    if (this.templates.player) {
      body = this.templates.player.clone(true)
    } else {
      body = new THREE.Mesh(new THREE.CylinderGeometry(2, 2, 0.6, 32), new THREE.MeshStandardMaterial({ color: '#2a2f3a', metalness: 0.6, roughness: 0.4 }))
      ;(body as THREE.Mesh).position.y = 0.3
    }
    const color = new THREE.Color(role === 'GK' ? GK_COLORS[team] : TEAM_COLORS[team])
    body.traverse((o) => {
      o.castShadow = true
      const m = o as THREE.Mesh
      if (m.isMesh) {
        const mats = (Array.isArray(m.material) ? m.material : [m.material]).map((mt) => {
          if (mt.name === 'TeamColor') {
            const c = (mt as THREE.MeshStandardMaterial).clone()
            c.color.copy(color); c.emissive?.copy(color)
            return c
          }
          return mt
        })
        m.material = Array.isArray(m.material) ? mats : mats[0]
      }
    })
    root.add(body)

    const ice = new THREE.Mesh(new THREE.IcosahedronGeometry(2.6, 0), new THREE.MeshStandardMaterial({ color: '#aeeaff', transparent: true, opacity: 0.55, flatShading: true }))
    ice.position.y = 1.2; ice.visible = false; root.add(ice)
    const shield = new THREE.Mesh(new THREE.SphereGeometry(3, 24, 16), new THREE.MeshBasicMaterial({ color: '#7dffb0', transparent: true, opacity: 0.22, depthWrite: false }))
    shield.position.y = 1; shield.visible = false; root.add(shield)
    const compile = new THREE.Mesh(new THREE.TorusGeometry(2.8, 0.08, 6, 6), new THREE.MeshBasicMaterial({ color: '#ffd23f' }))
    compile.position.y = 2.2; compile.rotation.x = Math.PI / 2; compile.visible = false; root.add(compile)
    const ring = new THREE.Mesh(new THREE.RingGeometry(2.7, 3.3, 40), new THREE.MeshBasicMaterial({ color: TEAM_COLORS[team], transparent: true, opacity: 0.9, side: THREE.DoubleSide }))
    ring.rotation.x = -Math.PI / 2; ring.position.y = 0.05; ring.visible = false; root.add(ring)
    this.scene.add(root)

    const label = document.createElement('div')
    label.className = 'tag'
    label.style.setProperty('--team', TEAM_COLORS[team])
    label.style.display = 'none'
    const nm = document.createElement('span')
    const castEl = document.createElement('div')
    castEl.className = 'cast'
    const bubble = document.createElement('div')
    bubble.className = 'bubble'
    label.append(nm, bubble, castEl)
    this.overlay.appendChild(label)
    return { root, body, target: new THREE.Vector3(), yaw: 0, label, nm, castEl, bubble, ice, shield, compile, ring, team, controlled: false }
  }

  private dropPlayer(id: string, p: PlayerObj) {
    p.label.remove()
    this.players.delete(id)
  }

  /** Trainer preview: play a forged skill's visual on the pitch. */
  previewSkill(sk: SkillInfo) {
    this.effects.spawn(sk.spec.visual.code, {
      origin: { x: -8, z: 0 }, aim: { x: 1, z: 0 }, reach: sk.reach || 8,
      color: sk.spec.visual.color || '#ffffff', duration: (sk.spec.visual.duration_ms || 800) / 1000,
    })
  }

  setSkills(list: SkillInfo[]) { this.skills = new Map(list.map((s) => [s.id, s])) }

  update(s: Snapshot) {
    this.pendingSnap = s
    const agents = new Map(s.agents.map((a) => [a.id, a]))
    for (const p of s.players) {
      let o = this.players.get(p.id)
      if (!o) {
        o = this.makePlayer(p.id, p.team, p.role)
        o.root.position.set(p.pos.x, 0, p.pos.z)
        this.players.set(p.id, o)
      }
      const ag = p.agent ? agents.get(p.agent) : undefined
      o.target.set(p.pos.x, 0, p.pos.z)
      o.yaw = -Math.atan2(p.face.z, p.face.x)
      o.ice.visible = (p.status?.frozen ?? 0) > 0
      o.shield.visible = (p.status?.shield ?? 0) > 0
      o.compile.visible = (ag?.compile_left ?? 0) > 0
      o.controlled = !!ag
      const slowed = (p.status?.slowed ?? 0) > 0
      let sr = this.slowRings.get(p.id)
      if (slowed && !sr) {
        sr = new THREE.Mesh(new THREE.RingGeometry(2.2, 3.1, 32), new THREE.MeshBasicMaterial({ color: '#6c8cff', transparent: true, opacity: 0.7, side: THREE.DoubleSide }))
        sr.rotation.x = -Math.PI / 2; sr.position.y = 0.08
        o.root.add(sr); this.slowRings.set(p.id, sr)
      }
      if (sr) sr.visible = slowed
      o.hasted = (p.status?.hasted ?? 0) > 0
      o.ring.visible = !!ag
      if (ag) {
        if (o.nm.textContent !== ag.name) o.nm.textContent = ag.name
        o.bubble.textContent = ag.message ?? ''
        o.bubble.style.opacity = ag.message ? '1' : '0'
      }
      o.label.style.display = ag ? 'block' : 'none'
    }
    this.ballTarget.set(s.ball_pos.x, s.ball_h ?? 1, s.ball_pos.z)

    const pset = new Set<number>()
    for (const pr of s.projectiles) {
      pset.add(pr.id)
      let m = this.projs.get(pr.id)
      if (!m) {
        m = new THREE.Mesh(new THREE.SphereGeometry(1, 16, 12), new THREE.MeshBasicMaterial({ color: this.skillColor(pr.skill, TEAM_COLORS[pr.team]) }))
        this.scene.add(m); this.projs.set(pr.id, m)
      }
      m.scale.setScalar(pr.radius)
      m.position.set(pr.pos.x, 1.2, pr.pos.z)
      this.fx.dot(pr.pos.x, 1.2, pr.pos.z, this.skillColor(pr.skill, '#aeeaff'), pr.radius * 0.6, 0.5)
    }
    for (const [id, m] of this.projs) if (!pset.has(id)) { this.scene.remove(m); m.geometry.dispose(); this.projs.delete(id) }

    const wset = new Set<number>()
    for (const w of s.walls) {
      wset.add(w.id)
      let m = this.walls.get(w.id)
      if (!m) {
        const len = Math.hypot(w.b.x - w.a.x, w.b.z - w.a.z)
        m = new THREE.Mesh(new THREE.BoxGeometry(len, 3, 0.8), new THREE.MeshStandardMaterial({ color: '#8affd6', transparent: true, opacity: 0.55, emissive: '#1d6e55' }))
        m.position.set((w.a.x + w.b.x) / 2, 1.5, (w.a.z + w.b.z) / 2)
        m.rotation.y = -Math.atan2(w.b.z - w.a.z, w.b.x - w.a.x)
        this.scene.add(m); this.walls.set(w.id, m)
      }
    }
    for (const [id, m] of this.walls) if (!wset.has(id)) { this.scene.remove(m); m.geometry.dispose(); this.walls.delete(id) }

    for (const e of s.events ?? []) this.onEvent(e)
  }

  private onEvent(e: GameEvent) {
    const d = e.data ?? {}
    if (e.type === 'skill_cast') {
      const sk = this.skills.get(d.skill)
      const owner = this.players.get(d.body)
      if (owner) {
        owner.castEl.textContent = `⚡ ${d.name}`
        owner.castEl.style.opacity = '1'
        clearTimeout(owner.castTimer)
        owner.castTimer = setTimeout(() => (owner.castEl.style.opacity = '0'), 1800)
      }
      const color = sk?.spec.visual.color || TEAM_COLORS[owner?.team ?? 0]
      this.effects.spawn(sk?.spec.visual.code ?? null, {
        origin: d.origin, aim: d.aim, reach: d.reach || 6, color, duration: (d.duration_ms || 800) / 1000,
      })
    } else if (e.type === 'goal') {
      this.shake = 1.2
    } else if (e.type === 'kick') {
      const p = this.players.get(d.player)
      if (p?.controlled) this.effects.spawn(null, { origin: { x: p.root.position.x, z: p.root.position.z }, aim: { x: 1, z: 0 }, reach: 3, color: '#ffffff', duration: 0.3 })
    } else if (e.type === 'impact') {
      this.onImpact(d)
    } else if (e.type === 'status') {
      const p = this.players.get(d.player)
      if (p) this.onStatus(p, d)
    } else if (e.type === 'shield_block') {
      const p = this.players.get(d.player)
      if (p) {
        this.fx.ring(p.root.position.x, p.root.position.z, '#7dffb0', 5, 0.5, 1)
        this.fx.text(p.root.position.x, 1, p.root.position.z, 'Blocked', '#7dffb0')
      }
    } else if (e.type === 'switch') {
      const p = this.players.get(d.player)
      if (p) this.effects.spawn(null, { origin: { x: p.root.position.x, z: p.root.position.z }, aim: { x: 1, z: 0 }, reach: 5, color: TEAM_COLORS[p.team], duration: 0.6 })
    } else if (e.type === 'projectile_hit') {
      const c = this.skillColor(d.skill, '#aeeaff')
      this.fx.burst(d.pos.x, 1.2, d.pos.z, c, 22, 0.8, 12)
      this.fx.ring(d.pos.x, d.pos.z, c, 6, 0.6)
      if (d.victim) this.fx.text(d.pos.x, 1, d.pos.z, 'Hit!', c)
      this.shake = Math.max(this.shake, 0.45)
    }
  }

  private skillColor(id: string | undefined, fallback: string) {
    return (id && this.skills.get(id)?.spec.visual.color) || fallback
  }

  private onStatus(p: PlayerObj, d: Record<string, any>) {
    const x = p.root.position.x, z = p.root.position.z
    const c = this.skillColor(d.skill, '#ffffff')
    if (d.status === 'freeze') { this.fx.shards(x, z, '#aeeaff'); this.fx.ring(x, z, '#aeeaff', 7, 0.6); this.fx.text(x, 1, z, 'Frozen', '#aeeaff'); this.shake = Math.max(this.shake, 0.3) }
    else if (d.status === 'slow') { this.fx.ring(x, z, '#6c8cff', 5, 0.8); this.fx.text(x, 1, z, 'Slowed', '#9db4ff') }
    else if (d.status === 'haste') { this.fx.burst(x, 1, z, c === '#ffffff' ? '#ffe14a' : c, 14, 0.6, 9); this.fx.text(x, 1, z, 'Haste!', '#ffe14a') }
    else if (d.status === 'shield') { this.fx.ring(x, z, '#7dffb0', 6, 0.6, 1); this.fx.text(x, 1, z, 'Shield', '#7dffb0') }
  }

  /** What a skill actually did, from the server's impact events. */
  private onImpact(d: Record<string, any>) {
    const c = this.skillColor(d.skill, '#ffffff')
    if (d.kind === 'ball') {
      const y = this.ball.position.y
      this.fx.burst(d.pos.x, y, d.pos.z, c, 26, 0.8, 16, d.dir)
      this.fx.ring(d.pos.x, d.pos.z, c, 7, 0.55)
      this.fx.text(d.pos.x, y, d.pos.z, d.lift > 0 ? `Lofted +${Math.round(d.lift)}` : `Launched +${Math.round(d.power)}`, c)
      this.trailColor = c
      this.trailUntil = performance.now() + 3000
      this.shake = Math.max(this.shake, 0.5)
    } else if (d.kind === 'shove') {
      this.fx.burst(d.pos.x, 1, d.pos.z, c, 16, 0.6, 12, d.dir)
      this.fx.ring(d.pos.x, d.pos.z, c, 5, 0.5)
      this.fx.text(d.pos.x, 1, d.pos.z, 'Shoved', c)
    } else if (d.kind === 'dash') {
      this.fx.ring(d.pos.x, d.pos.z, c, 4, 0.4)
      this.ghostClock = 0.6
    } else if (d.kind === 'magnet') {
      const p = this.players.get(d.target)
      if (p) this.fx.field(() => p.root.position, d.radius, c, d.duration)
      this.fx.text(d.pos.x, 1, d.pos.z, 'Magnet', c)
    } else if (d.kind === 'wall') {
      for (const pt of [d.a, d.b]) this.fx.pillar(pt.x, pt.z, c)
      this.fx.burst(d.pos.x, 1.5, d.pos.z, c, 20, 0.7, 8)
      this.fx.text(d.pos.x, 2, d.pos.z, 'Wall', c)
    }
  }

  private resize() {
    const el = this.canvas.parentElement ?? this.canvas
    const w = el.clientWidth, h = el.clientHeight
    if (!w || !h) return
    this.renderer.setSize(w, h, false)
    this.camera.aspect = w / h
    this.camera.updateProjectionMatrix()
    if (!this.userMoved) this.fitCamera()
  }

  /** Frame the whole pitch (100 wide) for the current aspect ratio. */
  private fitCamera() {
    const halfV = THREE.MathUtils.degToRad(this.camera.fov / 2)
    const need = Math.max(62 / (Math.tan(halfV) * this.camera.aspect), 40 / Math.tan(halfV))
    const d = Math.min(150, need * 0.9)
    this.camera.position.set(0, d * 0.72, d * 0.62 + 4)
    this.controls.target.set(0, 0, 4)
  }

  private frame = (now: number) => {
    if (this.disposed) return
    this.raf = requestAnimationFrame(this.frame)
    const dt = Math.min(0.05, (now - this.last) / 1000)
    this.last = now
    const k = 1 - Math.exp(-dt * 16)

    for (const p of this.players.values()) {
      if (p.root.position.distanceTo(p.target) > 15) p.root.position.copy(p.target)
      p.root.position.lerp(p.target, k)
      let dy = p.yaw - p.body.rotation.y
      dy = Math.atan2(Math.sin(dy), Math.cos(dy))
      p.body.rotation.y += dy * k
      p.compile.rotation.z += dt * 6
      p.ice.rotation.y += dt
    }
    const before = this.ball.position.clone()
    if (before.distanceTo(this.ballTarget) > 15) this.ball.position.copy(this.ballTarget)
    this.ball.position.lerp(this.ballTarget, k)
    const mv = this.ball.position.clone().sub(before)
    if (mv.lengthSq() > 1e-8) {
      const axis = new THREE.Vector3(mv.z, 0, -mv.x).normalize()
      this.ball.rotateOnWorldAxis(axis, mv.length())
    }

    const bspeed = mv.length() / Math.max(dt, 1e-3)
    const hot = performance.now() < this.trailUntil
    if (bspeed > 26 || this.ball.position.y > 3 || (hot && bspeed > 8)) {
      this.fx.dot(this.ball.position.x, this.ball.position.y, this.ball.position.z, hot ? this.trailColor : '#ffffff', hot ? 0.7 : 0.4, hot ? 0.7 : 0.35)
    }
    this.ghostClock = Math.max(0, this.ghostClock - dt)
    for (const p of this.players.values()) {
      if (p.hasted || (this.ghostClock > 0 && p.controlled)) this.fx.ghost(p.root.position.x, p.root.position.z, TEAM_COLORS[p.team])
    }
    this.fx.update(dt, this.camera, this.canvas.clientWidth, this.canvas.clientHeight)
    const h = Math.max(0, this.ball.position.y - 1)
    this.shadow.position.x = this.ball.position.x
    this.shadow.position.z = this.ball.position.z
    this.shadow.scale.setScalar(1.1 / (1 + h * 0.12))
    ;(this.shadow.material as THREE.MeshBasicMaterial).opacity = 0.38 / (1 + h * 0.15)
    this.effects.update(dt)
    this.controls.update()
    if (this.shake > 0) {
      this.camera.position.x += (Math.random() - 0.5) * this.shake
      this.camera.position.y += (Math.random() - 0.5) * this.shake
      this.shake = Math.max(0, this.shake - dt * 2.5)
    }
    this.renderer.render(this.scene, this.camera)
    this.placeLabels()
  }

  private placeLabels() {
    const w = this.canvas.clientWidth, h = this.canvas.clientHeight
    const v = new THREE.Vector3()
    for (const p of this.players.values()) {
      v.copy(p.root.position); v.y = 4.2
      v.project(this.camera)
      if (!p.controlled) continue
      p.label.style.display = v.z < 1 ? 'block' : 'none'
      p.label.style.transform = `translate(-50%, -100%) translate(${((v.x + 1) / 2) * w}px, ${((1 - v.y) / 2) * h}px)`
    }
  }

  dispose() {
    this.disposed = true
    cancelAnimationFrame(this.raf)
    this.ro.disconnect()
    this.controls.dispose()
    this.fx.dispose()
    for (const p of this.players.values()) p.label.remove()
    this.renderer.dispose()
  }
}
