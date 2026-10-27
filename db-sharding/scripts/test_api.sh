#!/bin/bash
set -e

BASE_URL="http://localhost:8080"

echo "================================================================"
echo "          DATABASE SHARDING & READ REPLICA API TEST"
echo "================================================================"

# 1. Health check
echo -e "\n1. Checking System Health via Load Balancer ($BASE_URL/health):"
curl -s "$BASE_URL/health" | jq .

# 2. Direct Backend Checks
echo -e "\n2. Verifying all 3 backend servers directly:"
for port in 8081 8082 8083; do
  echo -n "Port $port: "
  curl -s "http://localhost:$port/health" | jq -c '{server: .server, status: .status}'
done

# 3. Insert even and odd IDs
echo -e "\n3. Testing Write Routing (POST /users) to Shard Masters:"

users=(
  '{"id": 100, "name": "Alice Even", "email": "alice@shard1.com"}'
  '{"id": 101, "name": "Bob Odd", "email": "bob@shard2.com"}'
  '{"id": 102, "name": "Charlie Even", "email": "charlie@shard1.com"}'
  '{"id": 103, "name": "Diana Odd", "email": "diana@shard2.com"}'
  '{"id": 104, "name": "Evan Even", "email": "evan@shard1.com"}'
  '{"id": 105, "name": "Fiona Odd", "email": "fiona@shard2.com"}'
)

for u in "${users[@]}"; do
  echo "--- Inserting: $u ---"
  curl -s -X POST "$BASE_URL/users" \
    -H "Content-Type: application/json" \
    -d "$u" | jq -c '{server: .server, routing: .routing, user_id: .data.id}'
done

# 4. Read users back from read replicas
echo -e "\n4. Testing Read Routing (GET /users/{id}) from Read Replicas:"
for id in 100 101 102 103 104 105; do
  echo -n "Reading User $id -> "
  curl -s "$BASE_URL/users/$id" | jq -c '{server: .server, routing: .routing, name: .data.name}'
done

# 5. Test direct read on specific backend instances
echo -e "\n5. Querying specific backend instances directly:"
echo -n "Direct query to Backend 1 (port 8081) for User 100 (Shard 1): "
curl -s "http://localhost:8081/users/100" | jq -c '{server: .server, target_db: .routing.target_db, name: .data.name}'

echo -n "Direct query to Backend 2 (port 8082) for User 101 (Shard 2): "
curl -s "http://localhost:8082/users/101" | jq -c '{server: .server, target_db: .routing.target_db, name: .data.name}'

echo -n "Direct query to Backend 3 (port 8083) for User 104 (Shard 1): "
curl -s "http://localhost:8083/users/104" | jq -c '{server: .server, target_db: .routing.target_db, name: .data.name}'

# 6. Database Verification across all 4 databases
echo -e "\n6. Final Database Distribution & Replication Stats ($BASE_URL/stats):"
curl -s "$BASE_URL/stats" | jq .

echo -e "\n================================================================"
echo "              ALL TESTS COMPLETED SUCCESSFULLY!"
echo "================================================================"
