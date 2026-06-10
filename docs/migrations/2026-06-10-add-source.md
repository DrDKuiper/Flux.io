# Migration: add `source` column (2026-06-10)

Fresh installs get this from the init scripts. Existing ClickHouse deployments must run:

```sql
ALTER TABLE fluxio.network_flows   ADD COLUMN IF NOT EXISTS source String;
ALTER TABLE fluxio.suricata_alerts ADD COLUMN IF NOT EXISTS source String;
```

Postgres gets the `sources` table automatically via `CREATE TABLE IF NOT EXISTS`
on container start (the init script runs only on first boot, so for an existing
Postgres volume run the `CREATE TABLE` from `db/postgres/init-db.sql` manually).
