#!/bin/bash
set -e

echo ">>> Initializing Master Database: $POSTGRES_DB"

# Create replication user and base tables
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
    CREATE ROLE $REPL_USER WITH REPLICATION LOGIN PASSWORD '$REPL_PASSWORD';
    GRANT CONNECT ON DATABASE $POSTGRES_DB TO $REPL_USER;

    CREATE TABLE IF NOT EXISTS users (
        id BIGINT PRIMARY KEY,
        name VARCHAR(100) NOT NULL,
        email VARCHAR(100) NOT NULL,
        created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
    );
EOSQL

# Allow replication and normal connections from any host in the docker network
echo "host replication $REPL_USER 0.0.0.0/0 md5" >> "$PGDATA/pg_hba.conf"
echo "host all all 0.0.0.0/0 md5" >> "$PGDATA/pg_hba.conf"

echo ">>> Master Database initialization completed successfully."
