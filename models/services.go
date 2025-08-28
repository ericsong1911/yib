// yib/models/services.go
package models

import (
	"crypto/subtle"
	"fmt"
	mrand "math/rand"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"
)

// --- Stateful Services ---

type RateLimiter struct {
	Mu          sync.RWMutex
	Limiters    map[string]*rate.Limiter
	LastSeen    map[string]time.Time
	rate        rate.Limit
	burst       int
	pruneTicker *time.Ticker
	expireAfter time.Duration
}

type ChallengeStore struct {
	Mu         sync.RWMutex
	Challenges map[string]string
}

// --- Rate Limiter Methods ---

// NewRateLimiter creates and starts a new configurable rate limiter.
func NewRateLimiter(every time.Duration, burst int, pruneInterval, expireAfter time.Duration) *RateLimiter {
	rl := &RateLimiter{
		Limiters:    make(map[string]*rate.Limiter),
		LastSeen:    make(map[string]time.Time),
		rate:        rate.Every(every),
		burst:       burst,
		pruneTicker: time.NewTicker(pruneInterval),
		expireAfter: expireAfter,
	}
	go rl.cleanup()
	return rl
}

// GetLimiter retrieves or creates a rate limiter for a given IP address.
func (rl *RateLimiter) GetLimiter(ip string) *rate.Limiter {
	rl.Mu.Lock()
	defer rl.Mu.Unlock()
	limiter, exists := rl.Limiters[ip]
	if !exists {
		limiter = rate.NewLimiter(rl.rate, rl.burst)
		rl.Limiters[ip] = limiter
	}
	rl.LastSeen[ip] = time.Now()
	return limiter
}

// cleanup periodically removes old entries from the rate limiter maps.
func (rl *RateLimiter) cleanup() {
	for range rl.pruneTicker.C {
		rl.Mu.Lock()
		cutoff := time.Now().Add(-rl.expireAfter)
		for ip, lastSeen := range rl.LastSeen {
			if lastSeen.Before(cutoff) {
				delete(rl.Limiters, ip)
				delete(rl.LastSeen, ip)
			}
		}
		rl.Mu.Unlock()
	}
}

// --- Challenge Store Methods ---

// NewChallengeStore creates and starts a new challenge store.
func NewChallengeStore() *ChallengeStore {
	return &ChallengeStore{Challenges: make(map[string]string)}
}

// GenerateChallenge creates a new math question challenge.
func (cs *ChallengeStore) GenerateChallenge() (token, question string) {
	a, b := mrand.Intn(10)+1, mrand.Intn(10)+1
	answer := strconv.Itoa(a + b)
	question = fmt.Sprintf("What is %d + %d?", a, b)
	token = uuid.New().String()

	cs.Mu.Lock()
	cs.Challenges[token] = answer
	cs.Mu.Unlock()

	time.AfterFunc(5*time.Minute, func() {
		cs.Mu.Lock()
		delete(cs.Challenges, token)
		cs.Mu.Unlock()
	})
	return token, question
}

// Verify checks if a challenge answer is correct for a given token.
func (cs *ChallengeStore) Verify(token, answer string) bool {
	cs.Mu.Lock()
	defer cs.Mu.Unlock()

	correctAnswer, exists := cs.Challenges[token]

	delete(cs.Challenges, token)

	if !exists {
		return false // Token was invalid, expired, or already used.
	}

	return subtle.ConstantTimeCompare([]byte(answer), []byte(correctAnswer)) == 1
}
