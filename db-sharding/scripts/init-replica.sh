#!/bin/sh
set -e

echo ">>> Starting Replica setup..."
echo "Waiting for Master ($MASTER_HOST:5432) to be ready and initialized..."

while true; do
  if PGPASSWORD="$REPL_PASSWORD" psql -h "$MASTER_HOST" -p 5432 -U "$REPL_USER" -d "$POSTGRES_DB" -c 'SELECT 1;' >/dev/null 2>&1; then
    echo ">>> Master is online and ready!"
    break
  fi
  echo "Still waiting for master ($MASTER_HOST)..."
  sleep 1
done

if [ ! -s "$PGDATA/PG_VERSION" ]; then
  echo ">>> Cloning data from Master ($MASTER_HOST) via pg_basebackup..."
  rm -rf "$PGDATA"/*
  PGPASSWORD="$REPL_PASSWORD" pg_basebackup -h "$MASTER_HOST" -p 5432 -U "$REPL_USER" -D "$PGDATA" -Fp -Xs -R
  chown -R postgres:postgres "$PGDATA"
  chmod 700 "$PGDATA"
  echo ">>> Basebackup complete."
fi

echo ">>> Starting PostgreSQL Replica in standby mode..."
exec su-exec postgres postgres
