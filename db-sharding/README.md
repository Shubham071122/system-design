# Simple Database Sharding & Read/Write Replication in Go

A clean, straightforward implementation of **database sharding** with **read/write splitting** across 2 shards (each with 1 Master and 1 Read Replica), running a single Go backend service across 3 server instances behind an Nginx load balancer.

---

## Architecture Overview

```
                      Client Request (cURL / API)
                                  │
                                  ▼
                     [ Nginx Load Balancer: 8080 ]
                     ┌────────────┼────────────┐
                     ▼            ▼            ▼
               [ Backend 1 ] [ Backend 2 ] [ Backend 3 ]
               (Port 8081)   (Port 8082)   (Port 8083)
                     │            │            │
         All 3 backend servers execute the same Sharding Router:
                     ┌────────────────────────┐
                     │   Sharding Key: ID     │
                     │  Even ID -> Shard 1    │
                     │  Odd  ID -> Shard 2    │
                     └────────────┬───────────┘
                                  │
          ┌───────────────────────┴───────────────────────┐
          ▼                                               ▼
     [ SHARD 1 ]                                     [ SHARD 2 ]
   Even User IDs                                    Odd User IDs
  ┌─────────────────────┐                         ┌─────────────────────┐
  │ shard1-master: 5431 │                         │ shard2-master: 5433 │
  │    (Writes Only)    │                         │    (Writes Only)    │
  └──────────┬──────────┘                         └──────────┬──────────┘
             │ Streaming Replication                         │ Streaming Replication
             ▼                                               ▼
  ┌─────────────────────┐                         ┌─────────────────────┐
  │ shard1-replica: 5432│                         │ shard2-replica: 5434│
  │    (Reads Only)     │                         │    (Reads Only)     │
  └─────────────────────┘                         └─────────────────────┘
```

---

## Key Concepts Implemented

1. **Simple Modulo Sharding (`id % 2`)**:
   ```go
   func (sm *ShardManager) GetShard(id int64) *Shard {
       if id%2 == 0 {
           return sm.shards[1] // Shard 1
       }
       return sm.shards[2]     // Shard 2
   }
   ```
2. **Read / Write Splitting**:
   - `POST /users` (Write): Always routes to the shard's **Master** DB.
   - `GET /users/{id}` (Read): Always routes to the shard's **Read Replica** DB.
3. **Automated PostgreSQL Streaming Replication**:
   - `shard1-replica` and `shard2-replica` stream WAL updates in real-time from their respective masters using `pg_basebackup -R`.
4. **3 Backend Server Instances**:
   - Running the exact same Go application, accessible via Nginx round-robin (`:8080`) or directly (`:8081`, `:8082`, `:8083`).
   - Every API response indicates which backend server handled the request (`server`) and which DB received the query (`target_db`).

---

## Quick Start

### 1. Start all containers
```bash
cd db-sharding
docker compose up -d
```

### 2. Run automated test suite
```bash
./scripts/test_api.sh
```

---

## API Endpoints & cURL Examples

### 1. Check System Health
```bash
curl http://localhost:8080/health
```

### 2. Create User (Write -> Master)
- **Even ID (goes to Shard 1 Master)**:
```bash
curl -X POST http://localhost:8080/users \
  -H "Content-Type: application/json" \
  -d '{"id": 100, "name": "Alice Even", "email": "alice@shard1.com"}'
```
Response:
```json
{
  "message": "User saved successfully",
  "server": "backend-2",
  "routing": {
    "shard_id": 1,
    "target_db": "shard1-master",
    "operation": "WRITE (Master)",
    "reason": "ID 100 % 2 == 0 -> Shard 1 (shard1-master)"
  },
  "data": { "id": 100, "name": "Alice Even", "email": "alice@shard1.com" }
}
```

- **Odd ID (goes to Shard 2 Master)**:
```bash
curl -X POST http://localhost:8080/users \
  -H "Content-Type: application/json" \
  -d '{"id": 101, "name": "Bob Odd", "email": "bob@shard2.com"}'
```
Response:
```json
{
  "message": "User saved successfully",
  "server": "backend-3",
  "routing": {
    "shard_id": 2,
    "target_db": "shard2-master",
    "operation": "WRITE (Master)",
    "reason": "ID 101 % 2 == 1 -> Shard 2 (shard2-master)"
  },
  "data": { "id": 101, "name": "Bob Odd", "email": "bob@shard2.com" }
}
```

### 3. Fetch User (Read -> Replica)
```bash
curl http://localhost:8080/users/100
```
Response:
```json
{
  "message": "User retrieved successfully",
  "server": "backend-1",
  "routing": {
    "shard_id": 1,
    "target_db": "shard1-replica",
    "operation": "READ (Replica)",
    "reason": "ID 100 % 2 == 0 -> Shard 1 (shard1-replica)"
  },
  "data": { "id": 100, "name": "Alice Even", "email": "alice@shard1.com" }
}
```

### 4. Direct Server Invocations
You can also bypass the load balancer and call any backend instance directly:
- Backend 1: `curl http://localhost:8081/users/100`
- Backend 2: `curl http://localhost:8082/users/101`
- Backend 3: `curl http://localhost:8083/users/100`

### 5. Inspect Shard Distribution Across All 4 Databases
```bash
curl http://localhost:8080/stats
```
Response:
```json
{
  "server": "backend-1",
  "sharding_rule": "Even IDs (id % 2 == 0) -> Shard 1, Odd IDs (id % 2 != 0) -> Shard 2",
  "shard_1": {
    "master (shard1-master)": { "user_count": 3, "user_ids": [100, 102, 104] },
    "replica (shard1-replica)": { "user_count": 3, "user_ids": [100, 102, 104] }
  },
  "shard_2": {
    "master (shard2-master)": { "user_count": 3, "user_ids": [101, 103, 105] },
    "replica (shard2-replica)": { "user_count": 3, "user_ids": [101, 103, 105] }
  }
}
```

---

## Stopping the Services
```bash
docker compose down -v
```
