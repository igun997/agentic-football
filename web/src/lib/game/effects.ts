import * as THREE from 'three'

// Skill visuals are written by AI agents. They only ever receive this reduced THREE surface
// (no loaders => no network access through THREE) and shadowed globals. This is best-effort
// isolation; a sandboxed iframe is the planned hardening step (see design doc).
const safeThree: Record<string, unknown> = {}
for (const [k, v] of Object.entries(THREE)) {
  if (/Loader|Audio|Listener|Cache|ImageUtils/.test(k)) continue
  safeThree[k] = v
}
Object.freeze(safeThree)

const SHADOWED = [
  'window', 'document', 'globalThis', 'self', 'top', 'parent', 'frames', 'fetch', 'XMLHttpRequest',
  'WebSocket', 'EventSource', 'localStorage', 'sessionStorage', 'indexedDB', 'setTimeout', 'setInterval',
  'requestAnimationFrame', 'postMessage', 'Worker', 'navigator', 'location', 'Reflect', 'Proxy', 'WebAssembly',
]

export interface EffectArgs {
  origin: { x: number; z: number }
  aim: { x: number; z: number }
  reach: number
  color: string
  duration: number // seconds
}

interface Instance { update?: (t: number, dt: number) => void }

const compiled = new Map<string, ((ctx: unknown) => Instance | void) | null>()

function compile(code: string) {
  if (compiled.has(code)) return compiled.get(code)!
  let fn: ((ctx: unknown) => Instance | void) | null = null
  try {
    const f = new Function(...SHADOWED, 'ctx', '"use strict";\n' + code)
    fn = (ctx) => f(...SHADOWED.map(() => undefined), ctx)
  } catch (e) {
    console.warn('skill visual failed to compile', e)
  }
  compiled.set(code, fn)
  return fn
}

interface Active { group: THREE.Group; inst: Instance | null; t0: number; dur: number }

const MAX_OBJECTS = 400

function disposeTree(o: THREE.Object3D) {
  o.traverse((c) => {
    const m = c as THREE.Mesh
    m.geometry?.dispose()
    const mat = m.material as THREE.Material | THREE.Material[] | undefined
    if (Array.isArray(mat)) mat.forEach((x) => x.dispose())
    else mat?.dispose()
  })
}

export class EffectManager {
  private active: Active[] = []
  private clock = 0
  constructor(private scene: THREE.Scene) {}

  /** Default look used when an agent's code is missing or crashes. */
  private fallback(group: THREE.Group, a: EffectArgs): Instance {
    const ring = new THREE.Mesh(
      new THREE.TorusGeometry(1, 0.15, 8, 40),
      new THREE.MeshBasicMaterial({ color: a.color, transparent: true }),
    )
    ring.rotation.x = Math.PI / 2
    ring.position.y = 0.4
    group.add(ring)
    return { update: (t) => { ring.scale.setScalar(1 + t * Math.max(2, a.reach)); (ring.material as THREE.Material).opacity = 1 - t } }
  }

  spawn(code: string | null, a: EffectArgs) {
    const group = new THREE.Group()
    group.position.set(a.origin.x, 0, a.origin.z)
    this.scene.add(group)
    let inst: Instance | null = null
    const fn = code ? compile(code) : null
    if (fn) {
      try {
        let s = 1234567
        const rand = () => ((s = (s * 1664525 + 1013904223) >>> 0) / 4294967296)
        inst = fn({ THREE: safeThree, group, origin: a.origin, aim: a.aim, reach: a.reach, color: a.color, duration: a.duration, rand }) ?? {}
        let n = 0
        group.traverse(() => n++)
        if (n > MAX_OBJECTS) throw new Error('too many objects')
      } catch (e) {
        console.warn('skill visual crashed on setup', e)
        disposeTree(group)
        group.clear()
        inst = null
      }
    }
    if (!inst) inst = this.fallback(group, a)
    this.active.push({ group, inst, t0: this.clock, dur: a.duration })
  }

  update(dt: number) {
    this.clock += dt
    this.active = this.active.filter((e) => {
      const t = (this.clock - e.t0) / e.dur
      let alive = t < 1
      if (alive && e.inst?.update) {
        try { e.inst.update(Math.min(1, t), dt) } catch (err) { console.warn('skill visual crashed', err); alive = false }
      }
      if (!alive) {
        this.scene.remove(e.group)
        disposeTree(e.group)
      }
      return alive
    })
  }
}
