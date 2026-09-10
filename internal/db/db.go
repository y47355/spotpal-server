// Package db SQLite 连接与迁移：WAL 单写多读模型。
package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Open 打开（必要时创建）数据目录下的 spotpal.db，应用 WAL 关键配置。
//
// 并发模型（详细设计 §2.3）：
//   - 写：SetMaxOpenConns(1) 全局单写连接，串行化杜绝 SQLITE_BUSY；
//   - 读：WAL 模式下读写不互斥，只读查询走独立池（Open 读连接并行）。
func Open(dataDir string) (*sql.DB, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	dsn := "file:" + filepath.Join(dataDir, "spotpal.db") +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(ON)" +
		"&_pragma=synchronous(NORMAL)"
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// 单写者：所有连接复用 1 条，天然串行。
	d.SetMaxOpenConns(1)
	if err := d.Ping(); err != nil {
		return nil, err
	}
	return d, nil
}

// Migrate 按文件名序执行 migrations 下全部 SQL（IF NOT EXISTS 幂等）。
func Migrate(d *sql.DB, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".sql" {
			continue
		}
		buf, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		if _, err := d.Exec(string(buf)); err != nil {
			return fmt.Errorf("apply %s: %w", e.Name(), err)
		}
	}
	return nil
}

// Seed 执行 seed 脚本（跳过 001 之外的 002_seed.sql）。
func Seed(d *sql.DB, dir string) error {
	buf, err := os.ReadFile(filepath.Join(dir, "002_seed.sql"))
	if err != nil {
		return err
	}
	if _, err := d.Exec(string(buf)); err != nil {
		return fmt.Errorf("seed: %w", err)
	}
	return nil
}
