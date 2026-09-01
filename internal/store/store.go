package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ncruces/go-sqlite3"
	sqlite3driver "github.com/ncruces/go-sqlite3/driver"

	"github.com/ExpTechTW/proxygate/internal/model"
)

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	db, err := sqlite3driver.Open(path, configureSQLite)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.initialize(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error { return s.db.Close() }

func configureSQLite(conn *sqlite3.Conn) error {
	if err := conn.BusyTimeout(5 * time.Second); err != nil {
		return err
	}
	return conn.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;`)
}

func (s *Store) initialize(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS nodes (
  ip TEXT PRIMARY KEY, host_name TEXT NOT NULL, port INTEGER NOT NULL,
  score INTEGER NOT NULL, ping_ms INTEGER NOT NULL,
  speed_bps INTEGER NOT NULL, country_long TEXT NOT NULL, country_short TEXT NOT NULL,
  sessions INTEGER NOT NULL, uptime_ms INTEGER NOT NULL, total_users INTEGER NOT NULL,
  total_traffic INTEGER NOT NULL, log_type TEXT NOT NULL, operator TEXT NOT NULL, message TEXT NOT NULL,
  openvpn_config TEXT NOT NULL, protocols TEXT NOT NULL, measured_bps INTEGER NOT NULL DEFAULT 0,
  measured_at TEXT NOT NULL DEFAULT '', speed_test_failed INTEGER NOT NULL DEFAULT 0,
  last_failure TEXT NOT NULL DEFAULT '', failure_reason TEXT NOT NULL DEFAULT '',
  refreshed_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS idx_nodes_speed ON nodes(speed_bps DESC);
CREATE INDEX IF NOT EXISTS idx_nodes_ping ON nodes(ping_ms ASC);
CREATE INDEX IF NOT EXISTS idx_nodes_score ON nodes(score DESC);
DELETE FROM nodes WHERE instr(ip, ':') > 0;
`); err != nil {
		return err
	}
	var currentColumns int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('nodes') WHERE name IN ('port','speed_test_failed')`).Scan(&currentColumns); err != nil {
		return err
	}
	if currentColumns != 2 {
		return errors.New("SQLite schema is outdated; recreate the database")
	}
	return nil
}

func (s *Store) ReplaceNodes(ctx context.Context, nodes []model.Node, refreshedAt time.Time, preserveIP string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE incoming_nodes AS SELECT * FROM nodes WHERE 0`); err != nil {
		return err
	}
	statement, err := tx.PrepareContext(ctx, `INSERT INTO incoming_nodes
	(ip,host_name,port,score,ping_ms,speed_bps,country_long,country_short,sessions,uptime_ms,total_users,total_traffic,log_type,operator,message,openvpn_config,protocols,measured_bps,measured_at,speed_test_failed,last_failure,failure_reason,refreshed_at)
	VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,COALESCE((SELECT measured_bps FROM nodes WHERE ip=?),0),COALESCE((SELECT measured_at FROM nodes WHERE ip=?),''),COALESCE((SELECT speed_test_failed FROM nodes WHERE ip=?),0),COALESCE((SELECT last_failure FROM nodes WHERE ip=?),''),COALESCE((SELECT failure_reason FROM nodes WHERE ip=?),''),?)`)
	if err != nil {
		return err
	}
	for _, node := range nodes {
		protocols, _ := json.Marshal(node.Protocols)
		if _, err := statement.ExecContext(ctx, node.IP, node.HostName, node.Port, node.Score, node.PingMS, node.SpeedBPS,
			node.CountryLong, node.CountryShort, node.Sessions, node.UptimeMS, node.TotalUsers, node.TotalTraffic,
			node.LogType, node.Operator, node.Message, node.OpenVPNConfig, string(protocols), node.IP, node.IP, node.IP, node.IP, node.IP, formatTime(refreshedAt)); err != nil {
			return err
		}
	}
	if err := statement.Close(); err != nil {
		return err
	}
	if preserveIP != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO incoming_nodes SELECT * FROM nodes WHERE ip=? AND NOT EXISTS (SELECT 1 FROM incoming_nodes WHERE ip=?)`, preserveIP, preserveIP); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM nodes; INSERT INTO nodes SELECT * FROM incoming_nodes; DROP TABLE incoming_nodes`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO metadata(key,value) VALUES('last_refresh',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, formatTime(refreshedAt)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteNodeIfStale(ctx context.Context, ip string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM nodes WHERE ip=? AND refreshed_at<>(SELECT value FROM metadata WHERE key='last_refresh')`, ip)
	return err
}

func (s *Store) Nodes(ctx context.Context, mode string, limit, offset int) ([]model.Node, error) {
	order := orderBy(mode)
	rows, err := s.db.QueryContext(ctx, `SELECT host_name,ip,port,score,ping_ms,speed_bps,country_long,country_short,sessions,uptime_ms,total_users,total_traffic,log_type,operator,message,openvpn_config,protocols,measured_bps,measured_at,speed_test_failed,last_failure,failure_reason,refreshed_at FROM nodes ORDER BY `+order+` LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]model.Node, 0)
	for rows.Next() {
		node, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, node)
	}
	return result, rows.Err()
}

func (s *Store) Node(ctx context.Context, ip string) (model.Node, error) {
	row := s.db.QueryRowContext(ctx, `SELECT host_name,ip,port,score,ping_ms,speed_bps,country_long,country_short,sessions,uptime_ms,total_users,total_traffic,log_type,operator,message,openvpn_config,protocols,measured_bps,measured_at,speed_test_failed,last_failure,failure_reason,refreshed_at FROM nodes WHERE ip=?`, ip)
	return scanNode(row)
}

type scanner interface{ Scan(...any) error }

func scanNode(row scanner) (model.Node, error) {
	var node model.Node
	var protocols, measuredAt, lastFailure, refreshedAt string
	err := row.Scan(&node.HostName, &node.IP, &node.Port, &node.Score, &node.PingMS, &node.SpeedBPS, &node.CountryLong,
		&node.CountryShort, &node.Sessions, &node.UptimeMS, &node.TotalUsers, &node.TotalTraffic, &node.LogType,
		&node.Operator, &node.Message, &node.OpenVPNConfig, &protocols, &node.MeasuredBPS, &measuredAt,
		&node.SpeedTestFailed, &lastFailure, &node.FailureReason, &refreshedAt)
	if err != nil {
		return node, err
	}
	_ = json.Unmarshal([]byte(protocols), &node.Protocols)
	node.MeasuredAt = parseTime(measuredAt)
	node.LastFailure = parseTime(lastFailure)
	node.RefreshedAt = parseTime(refreshedAt)
	return node, nil
}

func (s *Store) MarkFailure(ctx context.Context, ip, reason string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET last_failure=?, failure_reason=? WHERE ip=?`, formatTime(time.Now()), reason, ip)
	return err
}

func (s *Store) SaveMeasurement(ctx context.Context, ip string, bitsPerSecond int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET measured_bps=?, measured_at=?, speed_test_failed=0 WHERE ip=?`, bitsPerSecond, formatTime(time.Now()), ip)
	return err
}

func (s *Store) SaveMeasurementFailure(ctx context.Context, ip string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET measured_bps=0, measured_at=?, speed_test_failed=1 WHERE ip=?`, formatTime(time.Now()), ip)
	return err
}

func (s *Store) LastRefresh(ctx context.Context) time.Time {
	var value string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key='last_refresh'`).Scan(&value); err != nil {
		return time.Time{}
	}
	return parseTime(value)
}

func (s *Store) Metadata(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key=?`, key).Scan(&value)
	return value, err
}

func (s *Store) SetMetadata(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func orderBy(mode string) string {
	switch mode {
	case "ping":
		return "CASE WHEN ping_ms <= 0 THEN 1 ELSE 0 END, ping_ms ASC, score DESC"
	case "score":
		return "score DESC, speed_bps DESC"
	default:
		return "speed_bps DESC, score DESC"
	}
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}

func IsNotFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }
