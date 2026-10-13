package room

import (
	"crypto/rand"
	"errors"
	"sync"
	"time"

	"agentswars/internal/sim"
)

const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

type Auth struct {
	Room *Room
	Seat *Seat
	Role Role
}

type Manager struct {
	mu     sync.RWMutex
	rooms  map[string]*Room
	tokens map[string]Auth
}

func NewManager() *Manager {
	m := &Manager{rooms: map[string]*Room{}, tokens: map[string]Auth{}}
	go m.janitor()
	return m
}

func (m *Manager) Create(cfg Config) (*Room, error) {
	if err := cfg.Normalize(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := 0; i < 20; i++ {
		b := make([]byte, 6)
		_, _ = rand.Read(b)
		for j := range b {
			b[j] = codeAlphabet[int(b[j])%len(codeAlphabet)]
		}
		code := string(b)
		if _, taken := m.rooms[code]; !taken {
			r := newRoom(code, cfg)
			m.rooms[code] = r
			return r, nil
		}
	}
	return nil, errors.New("could not allocate room code")
}

func (m *Manager) Get(code string) *Room {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.rooms[code]
}

// Join seats an agent in the room identified by its invite code and registers its role tokens.
func (m *Manager) Join(code, name, provider, model string) (*Room, *Seat, *sim.Brief, error) {
	r := m.Get(code)
	if r == nil {
		return nil, nil, nil, errors.New("unknown invite code")
	}
	seat, brief, err := r.Join(name, provider, model)
	if err != nil {
		return nil, nil, nil, err
	}
	m.mu.Lock()
	for role, tok := range seat.Tokens {
		m.tokens[tok] = Auth{Room: r, Seat: seat, Role: role}
	}
	m.mu.Unlock()
	return r, seat, brief, nil
}

func (m *Manager) Auth(tok string) (Auth, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, ok := m.tokens[tok]
	return a, ok
}

func (m *Manager) janitor() {
	for range time.Tick(time.Minute) {
		m.mu.Lock()
		for code, r := range m.rooms {
			if r.idleFor() > 45*time.Minute {
				delete(m.rooms, code)
				for t, a := range m.tokens {
					if a.Room == r {
						delete(m.tokens, t)
					}
				}
			}
		}
		m.mu.Unlock()
	}
}
