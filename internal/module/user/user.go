// Package user 用户模块：注册/登录（验证码）、资料、偏好。
package user

import (
	"context"
	"database/sql"
	"errors"
	"math/rand"

	"github.com/spotpal/spotpal-server/internal/pkg/log"
)

// ErrNotFound 用户不存在。
var ErrNotFound = errors.New("user not found")

// User 用户聚合。
type User struct {
	ID          string
	Phone       string
	Nickname    string
	City        string
	CreditScore int
}

// Service 用户服务。
type Service struct{ db *sql.DB }

// New 构造。
func New(db *sql.DB) *Service { return &Service{db: db} }

// LoginOrRegister 手机号 + 验证码登录；不存在则注册（验证码校验见 api 层调用 sms）。
func (s *Service) LoginOrRegister(ctx context.Context, phone, nickname string) (User, error) {
	var u User
	err := s.db.QueryRowContext(ctx,
		`SELECT id, phone, nickname, city, credit_score FROM users WHERE phone = ?`, phone).
		Scan(&u.ID, &u.Phone, &u.Nickname, &u.City, &u.CreditScore)
	if err == nil {
		return u, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return u, err
	}
	// 注册
	id := "u_" + randID()
	if nickname == "" {
		nickname = "搭友" + id[len(id)-4:]
	}
	_, err = s.db.Exec(
		`INSERT INTO users (id, phone, nickname, city) VALUES (?,?,?,?)`,
		id, phone, nickname, "")
	if err != nil {
		return u, err
	}
	log.L.Info("user registered", "id", id)
	return User{ID: id, Phone: phone, Nickname: nickname, CreditScore: 620}, nil
}

// Get 按 ID 查询。
func (s *Service) Get(ctx context.Context, id string) (User, error) {
	var u User
	err := s.db.QueryRowContext(ctx,
		`SELECT id, phone, nickname, city, credit_score FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Phone, &u.Nickname, &u.City, &u.CreditScore)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

// Interests 返回用户兴趣标签。
func (s *Service) Interests(ctx context.Context, uid string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT tag FROM user_interest WHERE user_id = ? ORDER BY weight DESC`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tags []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err == nil {
			tags = append(tags, t)
		}
	}
	return tags, nil
}

// UpsertInterest 新增/更新兴趣权重。
func (s *Service) UpsertInterest(ctx context.Context, uid, tag string, weight float64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO user_interest (user_id, tag, weight) VALUES (?,?,?)
		 ON CONFLICT(user_id, tag) DO UPDATE SET weight = excluded.weight, updated_at = datetime('now')`,
		uid, tag, weight)
	return err
}

func randID() string {
	const hex = "0123456789abcdef"
	b := make([]byte, 12)
	for i := range b {
		b[i] = hex[rand.Intn(16)]
	}
	return string(b)
}
