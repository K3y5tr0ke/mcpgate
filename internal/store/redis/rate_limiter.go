package redis

import (
	"context"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// RateLimiter implements store.RateLimiter using a Redis sorted-set sliding window.
// Atomicity is guaranteed by a Lua script loaded once at construction time.
type RateLimiter struct {
	client *goredis.Client
	sha    string // EVALSHA digest of the sliding-window script
}

// slidingWindowLua is an atomic sliding-window rate limiter.
// KEYS[1] = sorted-set key
// ARGV[1] = now_ms        (current Unix time in milliseconds)
// ARGV[2] = window_ms     (window duration in milliseconds)
// ARGV[3] = limit         (maximum requests in window)
// ARGV[4] = member        (unique ID for this request, e.g. request UUID)
//
// Returns: {allowed, remaining, retry_after_ms}
//
//	allowed=1 means the request is permitted; 0 means it was rejected.
const slidingWindowLua = `
local key     = KEYS[1]
local now     = tonumber(ARGV[1])
local window  = tonumber(ARGV[2])
local limit   = tonumber(ARGV[3])
local member  = ARGV[4]

redis.call('ZREMRANGEBYSCORE', key, '-inf', now - window)
local count = tonumber(redis.call('ZCARD', key))

if count < limit then
    redis.call('ZADD', key, now, member)
    redis.call('PEXPIRE', key, window)
    return {1, limit - count - 1, 0}
else
    local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
    local retry_ms = 0
    if #oldest >= 2 then
        retry_ms = math.max(0, tonumber(oldest[2]) + window - now)
    end
    return {0, 0, retry_ms}
end
`

// NewRateLimiter creates a RateLimiter and pre-loads the Lua script into Redis.
func NewRateLimiter(client *goredis.Client) (*RateLimiter, error) {
	sha, err := client.ScriptLoad(context.Background(), slidingWindowLua).Result()
	if err != nil {
		return nil, fmt.Errorf("rate_limiter: load lua script: %w", err)
	}
	return &RateLimiter{client: client, sha: sha}, nil
}

// Allow checks whether the request identified by key is within the configured limits.
// RPM is checked first; if RPM denies, RPD is not checked.
// If Redis is unavailable, Allow returns (Allowed:true, err) — fail open.
func (r *RateLimiter) Allow(ctx context.Context, key string, limit store.RateLimit) (store.RateLimitResult, error) {
	now := time.Now()
	nowMS := now.UnixMilli()

	if limit.RPM > 0 {
		result, err := r.check(ctx, "ratelimit:rpm:"+key, nowMS, int64(60_000), int64(limit.RPM), now)
		if err != nil {
			return store.RateLimitResult{Allowed: true}, err
		}
		if !result.Allowed {
			return result, nil
		}
	}

	if limit.RPD > 0 {
		result, err := r.check(ctx, "ratelimit:rpd:"+key, nowMS, int64(24*60*60*1000), int64(limit.RPD), now)
		if err != nil {
			return store.RateLimitResult{Allowed: true}, err
		}
		if !result.Allowed {
			return result, nil
		}
	}

	return store.RateLimitResult{Allowed: true}, nil
}

// StartConcurrent increments the per-user concurrent-connection counter and
// checks it against limit. If the counter exceeds limit, it decrements
// immediately and returns (over_limit=true, nil, nil). Otherwise it returns
// (false, cleanup, nil) — the caller MUST defer cleanup() to decrement on disconnect.
// A 5-minute TTL guards against counter leaks on unclean client disconnects.
// Fail-open: Redis errors return (false, noop, err).
func (r *RateLimiter) StartConcurrent(ctx context.Context, key string, limit int) (overLimit bool, cleanup func(), err error) {
	if limit <= 0 {
		return false, func() {}, nil
	}

	redisKey := "concurrent:" + key
	ttl := 5 * time.Minute

	count, incrErr := r.client.Incr(ctx, redisKey).Result()
	if incrErr != nil {
		return false, func() {}, fmt.Errorf("rate_limiter: concurrent incr: %w", incrErr)
	}
	// Refresh the TTL on every connection to keep the key alive.
	r.client.Expire(ctx, redisKey, ttl) //nolint:errcheck

	if int(count) > limit {
		r.client.Decr(ctx, redisKey) //nolint:errcheck
		return true, func() {}, nil
	}

	cleanup = func() {
		r.client.Decr(context.Background(), redisKey) //nolint:errcheck
	}
	return false, cleanup, nil
}

// check runs the sliding-window Lua script for a single window (rpm or rpd).
func (r *RateLimiter) check(
	ctx context.Context,
	key string,
	nowMS, windowMS, limit int64,
	now time.Time,
) (store.RateLimitResult, error) {
	member := fmt.Sprintf("%d-%p", nowMS, &nowMS) // cheap unique-enough ID

	vals, err := r.client.EvalSha(ctx, r.sha,
		[]string{key},
		nowMS, windowMS, limit, member,
	).Slice()
	if err != nil {
		// Script may have been flushed (e.g. Redis restart). Reload and retry once.
		if goredis.HasErrorPrefix(err, "NOSCRIPT") {
			sha, lerr := r.client.ScriptLoad(ctx, slidingWindowLua).Result()
			if lerr != nil {
				return store.RateLimitResult{Allowed: true}, fmt.Errorf("rate_limiter: reload script: %w", lerr)
			}
			r.sha = sha
			vals, err = r.client.EvalSha(ctx, r.sha, []string{key}, nowMS, windowMS, limit, member).Slice()
			if err != nil {
				return store.RateLimitResult{Allowed: true}, fmt.Errorf("rate_limiter: eval: %w", err)
			}
		} else {
			return store.RateLimitResult{Allowed: true}, fmt.Errorf("rate_limiter: eval: %w", err)
		}
	}

	if len(vals) < 3 {
		return store.RateLimitResult{Allowed: true}, fmt.Errorf("rate_limiter: unexpected script response length %d", len(vals))
	}

	allowed := toInt64(vals[0]) == 1
	remaining := toInt64(vals[1])
	retryMS := toInt64(vals[2])

	var retryAfter time.Duration
	var resetAt time.Time
	if !allowed && retryMS > 0 {
		retryAfter = time.Duration(retryMS) * time.Millisecond
		resetAt = now.Add(retryAfter)
	}

	return store.RateLimitResult{
		Allowed:    allowed,
		Remaining:  remaining,
		ResetAt:    resetAt,
		RetryAfter: retryAfter,
	}, nil
}

func toInt64(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	default:
		return 0
	}
}
