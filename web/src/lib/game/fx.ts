import * as THREE from 'three'

// Built-in visual feedback for what skills DO (as opposed to the agent-authored cast visual):
// bursts and rings where the ball is launched, shock rings on shoved players, ice shards on frozen
// players, magnet fields, trail dots, floating combat text. All driven by server events.

interface Item { obj: THREE.Object3D; t: number; dur: number; update: (k: number, dt: number) => void; follow?: () => THREE.Vector3 | null }
interface Text { el: HTMLElement; pos: THREE.Vector3; t: number; dur: number }

export class Fx {
  private items: Item[] = []
  private texts: Text[] = []
  constructor(private scene: THREE.Scene, private overlay: HTMLElement) {}

  private add(obj: THREE.Object3D, dur: number, update: Item['update'], follow?: Item['follow']) {
    this.scene.add(obj)
    this.items.push({ obj, t: 0, dur, update, follow })
  }

  /** Expanding ground shockwave ring. */
  ring(x: number, z: number, color: string, maxR: number, dur = 0.6, y = 0.25) {
    const m = new THREE.Mesh(new THREE.RingGeometry(0.85, 1, 48), new THREE.MeshBasicMaterial({ color, transparent: true, side: THREE.DoubleSide, depthWrite: false }))
    m.rotation.x = -Math.PI / 2
    m.position.set(x, y, z)
    this.add(m, dur, (k) => {
      const r = 0.5 + maxR * (1 - (1 - k) * (1 - k))
      m.scale.setScalar(r)
      ;(m.material as THREE.MeshBasicMaterial).opacity = 0.9 * (1 - k)
    })
  }

  /** Sparks flying out from a point; optionally biased along dir. */
  burst(x: number, y: number, z: number, color: string, n = 18, dur = 0.7, speed = 14, dir?: { x: number; z: number }) {
    const g = new THREE.Group()
    g.position.set(x, y, z)
    const parts: { m: THREE.Mesh; v: THREE.Vector3 }[] = []
    for (let i = 0; i < n; i++) {
      const m = new THREE.Mesh(new THREE.SphereGeometry(0.22 + Math.random() * 0.18, 6, 6), new THREE.MeshBasicMaterial({ color, transparent: true }))
      const a = Math.random() * Math.PI * 2
      const up = 2 + Math.random() * 7
      const v = new THREE.Vector3(Math.cos(a) * speed * Math.random(), up, Math.sin(a) * speed * Math.random())
      if (dir) { v.x += dir.x * speed * 0.9; v.z += dir.z * speed * 0.9 }
      g.add(m); parts.push({ m, v })
    }
    this.add(g, dur, (k, dt) => {
      for (const p of parts) {
        p.v.y -= 22 * dt
        p.m.position.addScaledVector(p.v, dt)
        if (p.m.position.y < -g.position.y + 0.1) { p.m.position.y = -g.position.y + 0.1; p.v.y *= -0.3 }
        ;(p.m.material as THREE.MeshBasicMaterial).opacity = 1 - k
        p.m.scale.setScalar(1 - k * 0.6)
      }
    })
  }

  /** Ice shards rising from a frozen player. */
  shards(x: number, z: number, color: string) {
    const g = new THREE.Group()
    g.position.set(x, 0, z)
    const bits: THREE.Mesh[] = []
    for (let i = 0; i < 10; i++) {
      const m = new THREE.Mesh(new THREE.OctahedronGeometry(0.5 + Math.random() * 0.4), new THREE.MeshBasicMaterial({ color, transparent: true }))
      const a = (i / 10) * Math.PI * 2
      m.userData = { a, r: 1 + Math.random() * 1.5, s: 3 + Math.random() * 4 }
      g.add(m); bits.push(m)
    }
    this.add(g, 0.9, (k) => {
      for (const m of bits) {
        const { a, r, s } = m.userData as { a: number; r: number; s: number }
        m.position.set(Math.cos(a) * r * (1 + k), 0.5 + k * s, Math.sin(a) * r * (1 + k))
        m.rotation.y = k * 9
        ;(m.material as THREE.MeshBasicMaterial).opacity = 1 - k * k
      }
    })
  }

  /** A swirling pull field that follows a player (magnet). */
  field(follow: () => THREE.Vector3 | null, radius: number, color: string, dur: number) {
    const g = new THREE.Group()
    const rings: THREE.Mesh[] = []
    for (let i = 0; i < 3; i++) {
      const m = new THREE.Mesh(new THREE.RingGeometry(0.93, 1, 56), new THREE.MeshBasicMaterial({ color, transparent: true, side: THREE.DoubleSide, depthWrite: false }))
      m.rotation.x = -Math.PI / 2
      g.add(m); rings.push(m)
    }
    this.add(g, dur, (k) => {
      rings.forEach((m, i) => {
        const p = (k * 3 * dur / 1.2 + i / 3) % 1 // each ring contracts towards the player
        m.scale.setScalar(radius * (1 - p) + 1.5)
        ;(m.material as THREE.MeshBasicMaterial).opacity = Math.min(1, p * 2) * 0.8 * (k > 0.85 ? (1 - k) / 0.15 : 1)
      })
    }, follow)
  }

  /** Fading dot used for ball and projectile trails. */
  dot(x: number, y: number, z: number, color: string, size = 0.5, dur = 0.5) {
    const m = new THREE.Mesh(new THREE.SphereGeometry(size, 6, 6), new THREE.MeshBasicMaterial({ color, transparent: true }))
    m.position.set(x, y, z)
    this.add(m, dur, (k) => { m.scale.setScalar(1 - k); (m.material as THREE.MeshBasicMaterial).opacity = 0.8 * (1 - k) })
  }

  /** Afterimage of a hasted disc. */
  ghost(x: number, z: number, color: string) {
    const m = new THREE.Mesh(new THREE.CylinderGeometry(2, 2, 0.4, 24), new THREE.MeshBasicMaterial({ color, transparent: true }))
    m.position.set(x, 0.3, z)
    this.add(m, 0.45, (k) => { (m.material as THREE.MeshBasicMaterial).opacity = 0.45 * (1 - k); m.scale.setScalar(1 - k * 0.3) })
  }

  /** Vertical beam + ring on a wall / ground spot. */
  pillar(x: number, z: number, color: string, h = 7, dur = 0.8) {
    const m = new THREE.Mesh(new THREE.CylinderGeometry(0.4, 1.1, h, 12, 1, true), new THREE.MeshBasicMaterial({ color, transparent: true, side: THREE.DoubleSide, depthWrite: false }))
    m.position.set(x, h / 2, z)
    this.add(m, dur, (k) => { m.scale.set(1 - k * 0.5, 1 + k * 0.4, 1 - k * 0.5); (m.material as THREE.MeshBasicMaterial).opacity = 0.7 * (1 - k) })
  }

  /** A glowing streak between two ground points (pass / launch). */
  streak(a: { x: number; z: number }, b: { x: number; z: number }, color: string, dur = 0.8, y = 1.2, arc = 0) {
    const pts: THREE.Vector3[] = []
    for (let i = 0; i <= 24; i++) {
      const t = i / 24
      pts.push(new THREE.Vector3(a.x + (b.x - a.x) * t, y + Math.sin(t * Math.PI) * arc, a.z + (b.z - a.z) * t))
    }
    const line = new THREE.Line(new THREE.BufferGeometry().setFromPoints(pts), new THREE.LineBasicMaterial({ color, transparent: true }))
    this.add(line, dur, (k) => { (line.material as THREE.LineBasicMaterial).opacity = 1 - k })
  }

  /** Floating combat text above a world position. */
  text(x: number, y: number, z: number, text: string, color: string) {
    const el = document.createElement('div')
    el.className = 'fx-text'
    el.textContent = text
    el.style.color = color
    this.overlay.appendChild(el)
    this.texts.push({ el, pos: new THREE.Vector3(x, y, z), t: 0, dur: 1.5 })
  }

  update(dt: number, camera: THREE.Camera, w: number, h: number) {
    this.items = this.items.filter((it) => {
      it.t += dt
      const k = Math.min(1, it.t / it.dur)
      if (it.follow) {
        const p = it.follow()
        if (p) it.obj.position.set(p.x, 0.3, p.z)
      }
      it.update(k, dt)
      if (k >= 1) {
        this.scene.remove(it.obj)
        it.obj.traverse((o) => {
          const m = o as THREE.Mesh
          m.geometry?.dispose()
          const mat = m.material as THREE.Material | undefined
          mat?.dispose()
        })
        return false
      }
      return true
    })
    const v = new THREE.Vector3()
    this.texts = this.texts.filter((tx) => {
      tx.t += dt
      const k = tx.t / tx.dur
      if (k >= 1) { tx.el.remove(); return false }
      v.copy(tx.pos); v.y += 3 + k * 4
      v.project(camera)
      tx.el.style.display = v.z < 1 ? 'block' : 'none'
      tx.el.style.opacity = String(k > 0.6 ? 1 - (k - 0.6) / 0.4 : 1)
      tx.el.style.transform = `translate(-50%, -50%) translate(${((v.x + 1) / 2) * w}px, ${((1 - v.y) / 2) * h}px) scale(${1 + 0.25 * Math.sin(Math.min(1, k * 4) * Math.PI)})`
      return true
    })
  }

  dispose() {
    for (const it of this.items) this.scene.remove(it.obj)
    for (const t of this.texts) t.el.remove()
    this.items = []; this.texts = []
  }
}
