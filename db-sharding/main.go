package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"time"

	_ "github.com/lib/pq"
)

// Shard holds connections to master (writes) and replica (reads)
type Shard struct {
	ID          int
	MasterName  string
	ReplicaName string
	Master      *sql.DB
	Replica     *sql.DB
}

// ShardManager manages all shards
type ShardManager struct {
	shards map[int]*Shard
}

// Simple Modulo Sharding:
// Even IDs -> Shard 1
// Odd IDs  -> Shard 2
func (sm *ShardManager) GetShard(id int64) *Shard {
	if id%2 == 0 {
		return sm.shards[1]
	}
	return sm.shards[2]
}

type User struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

type RoutingInfo struct {
	ShardID   int    `json:"shard_id"`
	TargetDB  string `json:"target_db"`
	Operation string `json:"operation"`
	Reason    string `json:"reason"`
}

type ApiResponse struct {
	Message string      `json:"message"`
	Server  string      `json:"server"`
	Routing RoutingInfo `json:"routing"`
	Data    any         `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

var (
	serverName   string
	shardManager *ShardManager
)

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func connectDB(name, url string) *sql.DB {
	var db *sql.DB
	var err error

	for attempts := 1; attempts <= 30; attempts++ {
		db, err = sql.Open("postgres", url)
		if err == nil {
			err = db.Ping()
			if err == nil {
				log.Printf("[%s] Successfully connected to %s", serverName, name)
				db.SetMaxOpenConns(10)
				db.SetMaxIdleConns(5)
				return db
			}
		}
		log.Printf("[%s] Waiting for %s (%d/30)... error: %v", serverName, name, attempts, err)
		time.Sleep(2 * time.Second)
	}

	log.Fatalf("[%s] Failed to connect to %s after 30 attempts: %v", serverName, name, err)
	return nil
}

func handleCreateUser(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req struct {
		ID    int64  `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ApiResponse{
			Server: serverName,
			Error:  "Invalid JSON payload: " + err.Error(),
		})
		return
	}

	// Auto-assign an ID if not provided
	if req.ID == 0 {
		req.ID = time.Now().UnixNano()/1e6 + int64(rand.Intn(100))
	}
	if req.Name == "" || req.Email == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ApiResponse{
			Server: serverName,
			Error:  "Both 'name' and 'email' are required",
		})
		return
	}

	// 1. Sharding Logic: route based on ID
	shard := shardManager.GetShard(req.ID)

	// 2. Read/Write Split: Writes ALWAYS go to Master
	query := `
		INSERT INTO users (id, name, email)
		VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET name = $2, email = $3
		RETURNING created_at;
	`
	var createdAt time.Time
	err := shard.Master.QueryRow(query, req.ID, req.Name, req.Email).Scan(&createdAt)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ApiResponse{
			Server: serverName,
			Error:  fmt.Sprintf("Failed to insert into %s: %v", shard.MasterName, err),
		})
		return
	}

	user := User{
		ID:        req.ID,
		Name:      req.Name,
		Email:     req.Email,
		CreatedAt: createdAt,
	}

	shardReason := fmt.Sprintf("ID %d %% 2 == %d -> Shard %d (%s)", req.ID, req.ID%2, shard.ID, shard.MasterName)

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(ApiResponse{
		Message: "User saved successfully",
		Server:  serverName,
		Routing: RoutingInfo{
			ShardID:   shard.ID,
			TargetDB:  shard.MasterName,
			Operation: "WRITE (Master)",
			Reason:    shardReason,
		},
		Data: user,
	})
}

func handleGetUser(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ApiResponse{
			Server: serverName,
			Error:  "Invalid user id: must be an integer",
		})
		return
	}

	// 1. Sharding Logic: route based on ID
	shard := shardManager.GetShard(id)

	// 2. Read/Write Split: Reads go to Read Replica
	query := `SELECT id, name, email, created_at FROM users WHERE id = $1`
	var user User
	err = shard.Replica.QueryRow(query, id).Scan(&user.ID, &user.Name, &user.Email, &user.CreatedAt)
	if err == sql.ErrNoRows {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(ApiResponse{
			Message: "User not found",
			Server:  serverName,
			Routing: RoutingInfo{
				ShardID:   shard.ID,
				TargetDB:  shard.ReplicaName,
				Operation: "READ (Replica)",
				Reason:    fmt.Sprintf("Checked Shard %d Replica (%s)", shard.ID, shard.ReplicaName),
			},
		})
		return
	} else if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ApiResponse{
			Server: serverName,
			Error:  fmt.Sprintf("Query failed on %s: %v", shard.ReplicaName, err),
		})
		return
	}

	shardReason := fmt.Sprintf("ID %d %% 2 == %d -> Shard %d (%s)", id, id%2, shard.ID, shard.ReplicaName)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(ApiResponse{
		Message: "User retrieved successfully",
		Server:  serverName,
		Routing: RoutingInfo{
			ShardID:   shard.ID,
			TargetDB:  shard.ReplicaName,
			Operation: "READ (Replica)",
			Reason:    shardReason,
		},
		Data: user,
	})
}

type ShardStats struct {
	Count int64   `json:"user_count"`
	IDs   []int64 `json:"user_ids"`
}

func queryStats(db *sql.DB) ShardStats {
	stats := ShardStats{IDs: []int64{}}
	rows, err := db.Query("SELECT id FROM users ORDER BY id ASC")
	if err != nil {
		return stats
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			stats.IDs = append(stats.IDs, id)
		}
	}
	stats.Count = int64(len(stats.IDs))
	return stats
}

func handleStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	result := map[string]any{
		"server": serverName,
		"sharding_rule": "Even IDs (id % 2 == 0) -> Shard 1, Odd IDs (id % 2 != 0) -> Shard 2",
		"shard_1": map[string]any{
			"master (shard1-master)":   queryStats(shardManager.shards[1].Master),
			"replica (shard1-replica)": queryStats(shardManager.shards[1].Replica),
		},
		"shard_2": map[string]any{
			"master (shard2-master)":   queryStats(shardManager.shards[2].Master),
			"replica (shard2-replica)": queryStats(shardManager.shards[2].Replica),
		},
	}

	json.NewEncoder(w).Encode(result)
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	status := "healthy"
	dbs := map[string]string{
		"shard1_master":  "ok",
		"shard1_replica": "ok",
		"shard2_master":  "ok",
		"shard2_replica": "ok",
	}

	if err := shardManager.shards[1].Master.Ping(); err != nil {
		dbs["shard1_master"] = err.Error()
		status = "degraded"
	}
	if err := shardManager.shards[1].Replica.Ping(); err != nil {
		dbs["shard1_replica"] = err.Error()
		status = "degraded"
	}
	if err := shardManager.shards[2].Master.Ping(); err != nil {
		dbs["shard2_master"] = err.Error()
		status = "degraded"
	}
	if err := shardManager.shards[2].Replica.Ping(); err != nil {
		dbs["shard2_replica"] = err.Error()
		status = "degraded"
	}

	json.NewEncoder(w).Encode(map[string]any{
		"server":    serverName,
		"status":    status,
		"databases": dbs,
	})
}

func main() {
	serverName = getEnv("SERVER_NAME", "backend-1")
	port := getEnv("PORT", "8080")

	log.Printf("[%s] Initializing Sharded Backend Server...", serverName)

	shard1MasterURL := getEnv("SHARD1_MASTER_URL", "postgres://postgres:postgres@shard1-master:5432/app_db?sslmode=disable")
	shard1ReplicaURL := getEnv("SHARD1_REPLICA_URL", "postgres://postgres:postgres@shard1-replica:5432/app_db?sslmode=disable")
	shard2MasterURL := getEnv("SHARD2_MASTER_URL", "postgres://postgres:postgres@shard2-master:5432/app_db?sslmode=disable")
	shard2ReplicaURL := getEnv("SHARD2_REPLICA_URL", "postgres://postgres:postgres@shard2-replica:5432/app_db?sslmode=disable")

	shard1 := &Shard{
		ID:          1,
		MasterName:  "shard1-master",
		ReplicaName: "shard1-replica",
		Master:      connectDB("shard1-master", shard1MasterURL),
		Replica:     connectDB("shard1-replica", shard1ReplicaURL),
	}

	shard2 := &Shard{
		ID:          2,
		MasterName:  "shard2-master",
		ReplicaName: "shard2-replica",
		Master:      connectDB("shard2-master", shard2MasterURL),
		Replica:     connectDB("shard2-replica", shard2ReplicaURL),
	}

	shardManager = &ShardManager{
		shards: map[int]*Shard{
			1: shard1,
			2: shard2,
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /users", handleCreateUser)
	mux.HandleFunc("GET /users/{id}", handleGetUser)
	mux.HandleFunc("GET /stats", handleStats)
	mux.HandleFunc("GET /health", handleHealth)

	addr := ":" + port
	log.Printf("[%s] Server listening on %s", serverName, addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("[%s] HTTP server exited: %v", serverName, err)
	}
}
