import type { Brief, RoomInfo, Snapshot } from './types'

async function j<T>(res: Response): Promise<T> {
  const body = await res.json()
  if (!res.ok) throw new Error(body.error ?? res.statusText)
  return body as T
}

const hostKey = (code: string) => `aw_host_${code}`
export const hostToken = (code: string): string => {
  try { return localStorage.getItem(hostKey(code)) ?? '' } catch { return '' }
}

export async function createRoom(cfg: { players_per_team: number; agents_per_team: number; duration_secs: number; time_scale: number }) {
  const r = await fetch('/api/rooms', { method: 'POST', body: JSON.stringify(cfg) }).then(j<RoomInfo & { host_token: string }>)
  try { localStorage.setItem(hostKey(r.code), r.host_token) } catch { /* private mode: host controls just won't persist */ }
  return r
}

export const getRoom = (code: string) => fetch(`/api/rooms/${code}`).then(j<RoomInfo>)

export const startRoom = (code: string) =>
  fetch(`/api/rooms/${code}/start`, { method: 'POST', headers: { 'X-Host-Token': hostToken(code) } }).then(j<RoomInfo>)

export const saveBrief = (code: string, team: number, index: number, brief: Brief) =>
  fetch(`/api/rooms/${code}/slots/${team}/${index}/brief`, {
    method: 'PUT', headers: { 'X-Host-Token': hostToken(code) }, body: JSON.stringify(brief),
  }).then(j<RoomInfo>)

export function streamRoom(code: string, onSnap: (s: Snapshot) => void): () => void {
  const es = new EventSource(`/api/rooms/${code}/stream`)
  es.onmessage = (e) => onSnap(JSON.parse(e.data))
  return () => es.close()
}
