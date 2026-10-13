export interface Vec { x: number; z: number }

export interface SkillView { slot: number; id: string; name: string; power: number; cooldown: number; cd_left: number; cast_cost: number }

export interface PlayerView {
  id: string; num: number; team: number; role: 'GK' | 'DEF' | 'MID' | 'FWD'
  pos: Vec; vel: Vec; face: Vec
  status?: Record<string, number>
  agent?: string
}

export interface BenchView {
  provider?: string; model?: string
  calls_per_sec: number; calls_avg: number; calls: number; errors: number
  turn_p50_ms: number; turn_p95_ms: number; reaction_p50_ms: number; author_avg_ms: number
  tokens_per_sec: number; tokens_out: number; tokens_in: number; reports: number
}
export interface BenchEntry extends BenchView { agent_id: string; name: string; team: number }

export interface SkillBrief {
  slot: number; concept: string; archetype: string; element: string; color: string; intensity: string; power_budget: number
}
export interface Brief {
  persona: { style: string; preferred_role: string; motto: string }
  skills: SkillBrief[]
  lock_loadout: boolean
}
export interface SlotInfo { team: number; index: number; brief: Brief; agent_id?: string; name?: string; ready: boolean }

export interface AgentView {
  id: string; name: string; team: number
  controls: string
  strikes: number
  message?: string
  compile_left?: number
  recoil_left?: number
  switch_cd?: number
  ready?: boolean
  style?: string
  motto?: string
  bench?: BenchView
  skills?: SkillView[]
}

export interface GameEvent { type: string; tick: number; data?: Record<string, any> }

export interface Snapshot {
  room: string; tick: number
  phase: 'lobby' | 'countdown' | 'playing' | 'goal' | 'finished'
  phase_left: number; time_left: number
  score: [number, number]; winner: number
  ball_pos: Vec; ball_vel: Vec
  restart?: { kind: string; team: number; spot: Vec }
  players: PlayerView[]
  agents: AgentView[]
  projectiles: { id: number; pos: Vec; radius: number; team: number; skill: string }[]
  walls: { id: number; a: Vec; b: Vec; left: number }[]
  events?: GameEvent[]
  skills_version: number
}

export interface SkillInfo {
  id: string; owner: string; slot: number
  spec: { name: string; description?: string; visual: { code: string; duration_ms: number; color?: string } }
  power: number; create_cost: number; cast_cost: number; cooldown: number; recoil: number; reach: number
}

export interface RoomInfo {
  code: string
  config: { players_per_team: number; agents_per_team: number; duration_secs: number; time_scale: number }
  phase: Snapshot['phase']
  seats: { agent_id: string; name: string; team: number }[]
  skills: SkillInfo[]
  feed: GameEvent[]
  snapshot: Snapshot
  seats_free: number
  slots: SlotInfo[]
  prep_deadline_ms?: number
  bench: BenchEntry[]
}
