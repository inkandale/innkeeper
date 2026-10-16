package plugins

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ProvisionDatabases gives each plugin that sets database = true its own
// schema and login role, and keeps the role's URL for Instance. Every step is
// idempotent and runs at each start, which also repairs a restored database.
func (r *Registry) ProvisionDatabases(ctx context.Context, dbURL string) error {
	var names []string
	for name, m := range r.Manifests() {
		if m.Database {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	u, err := url.Parse(dbURL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return errors.New("plugin databases need INNKEEPER_DATABASE_URL as a postgres:// URL")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// The API and worker can start together.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('innkeeper-plugin-db'))`); err != nil {
		return err
	}
	var database string
	var createRole, createSchema bool
	err = tx.QueryRow(ctx, `SELECT current_database(), rolsuper OR rolcreaterole, has_database_privilege(current_database(), 'CREATE')
		FROM pg_roles WHERE rolname = current_user`).Scan(&database, &createRole, &createSchema)
	if err != nil {
		return err
	}
	if !createRole {
		return errors.New("plugin databases need the database user to have CREATEROLE")
	}
	if !createSchema {
		return fmt.Errorf("plugin databases need the database user to have CREATE on database %s", database)
	}
	dsns := map[string]string{}
	for _, name := range names {
		schema, role := dbNames(database, name)
		if len(role) > 63 {
			return fmt.Errorf("plugin %s: role name %s is longer than PostgreSQL's 63 bytes", name, role)
		}
		pw := r.box.Derive("plugin-database:" + role)
		if err := provision(ctx, tx, database, schema, role, pw); err != nil {
			return fmt.Errorf("plugin %s database: %w", name, err)
		}
		pu := *u
		pu.User = url.UserPassword(role, pw)
		dsns[name] = pu.String()
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	r.mu.Lock()
	r.dsns = dsns
	r.mu.Unlock()
	return nil
}

func provision(ctx context.Context, tx pgx.Tx, database, schema, role, pw string) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT FROM pg_roles WHERE rolname = $1)`, role).Scan(&exists); err != nil {
		return err
	}
	// ALTER ROLE can't take parameters, so format quotes the password.
	var setPassword string
	if err := tx.QueryRow(ctx, `SELECT format('ALTER ROLE %I PASSWORD %L', $1::text, $2::text)`, role, pw).Scan(&setPassword); err != nil {
		return err
	}
	r, s, d := pgx.Identifier{role}.Sanitize(), pgx.Identifier{schema}.Sanitize(), pgx.Identifier{database}.Sanitize()
	var stmts []string
	if !exists {
		stmts = append(stmts, `CREATE ROLE `+r+` LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT`)
	}
	stmts = append(stmts, setPassword,
		`ALTER ROLE `+r+` SET search_path = `+s,
		// A non-superuser can only give the schema to a role it can SET ROLE to.
		`GRANT `+r+` TO CURRENT_USER`,
		`CREATE SCHEMA IF NOT EXISTS `+s,
		`ALTER SCHEMA `+s+` OWNER TO `+r,
		`GRANT CONNECT ON DATABASE `+d+` TO `+r)
	for _, q := range stmts {
		if _, err := tx.Exec(ctx, q); err != nil {
			return err
		}
	}
	// pg_restore into a database with another name leaves the schema's
	// objects owned by the old role or the core user, so the plugin couldn't
	// alter them in its own migrations.
	rows, err := tx.Query(ctx, reownSQL, schema, role)
	if err != nil {
		return err
	}
	reown, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, q := range reown {
		if _, err := tx.Exec(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

// reownSQL lists the statements that give schema $1's objects to role $2.
// Sequences a table owns, and types a table or range made, follow their owner.
const reownSQL = `
SELECT format('ALTER TABLE %s OWNER TO %I', c.oid::regclass, $2::text) FROM pg_class c
WHERE c.relnamespace = $1::text::regnamespace AND c.relowner <> $2::text::regrole
	AND c.relkind IN ('r', 'p', 'v', 'm', 'f', 'S')
	AND NOT (c.relkind = 'S' AND EXISTS (SELECT FROM pg_depend d
		WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid AND d.deptype IN ('a', 'i')))
UNION ALL
SELECT format('ALTER ROUTINE %s OWNER TO %I', p.oid::regprocedure, $2::text) FROM pg_proc p
WHERE p.pronamespace = $1::text::regnamespace AND p.proowner <> $2::text::regrole
UNION ALL
SELECT format('ALTER %s %s OWNER TO %I', CASE t.typtype WHEN 'd' THEN 'DOMAIN' ELSE 'TYPE' END, t.oid::regtype, $2::text)
FROM pg_type t
WHERE t.typnamespace = $1::text::regnamespace AND t.typowner <> $2::text::regrole
	AND (t.typtype IN ('e', 'd', 'r')
		OR (t.typtype = 'c' AND (SELECT relkind FROM pg_class WHERE oid = t.typrelid) = 'c'))`

// dbNames returns plugin's schema and login role in database. Roles are
// cluster-wide, so two databases on one server must not share one.
func dbNames(database, plugin string) (schema, role string) {
	schema = "plugin_" + strings.ReplaceAll(plugin, "-", "_")
	return schema, database + "_" + schema
}
