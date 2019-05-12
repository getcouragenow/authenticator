package test

import (
	"fmt"
	"math/rand"
	"strconv"
	"time"

	"github.com/go-redis/redis"
)

// NewRedisDB returns a redis DB for testing.
// We allocate a random DB to avoid race conditions
// in teardown/setup methods.
func NewRedisDB() (*redis.Client, error) {
	rand.Seed(time.Now().UnixNano())
	dbNo := rand.Intn(16)
	redisURL := fmt.Sprintf("redis://:swordfish@localhost:6379/%s", strconv.Itoa(dbNo))

	redisConfig, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, err
	}

	db := redis.NewClient(redisConfig)
	_, err = db.Ping().Result()
	if err != nil {
		db.Close()

		return nil, err
	}

	return db, nil
}
