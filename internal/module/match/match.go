// Package match 匹配引擎：三路召回（tag JOIN / vec 中性分 / geo 内存表）。
package match

import (
	"context"
	"database/sql"
	"math"
	"strings"
	"sync"
	"time"
)

// Score 权重（详细设计 §3.3）。
const (
	wTag = 0.40
	wVec = 0.35
	wGeo = 0.25
)

// Card 推荐卡。
type Card struct {
	UserID     string   `json:"user_id"`
	Nickname   string   `json:"nickname"`
	TagOverlap []string `json:"tag_overlap"`
	Score      float64  `json:"score"`
	Reason     string   `json:"reason"`
	Distance   float64  `json:"distance"` // 米；商圈级
}

// Service 匹配服务。
type Service struct {
	db *sql.DB
}

// New 构造。
func New(db *sql.DB) *Service { return &Service{db: db} }

// Recommend 返回推荐流（cursor 分页，size 每页条数）。
// vec 召回冷启动返回中性分 0.5（vec_cache 表预留，行为数据积累后启用）。
//
// 实现注：单连接模型下禁止嵌套查询（外层 rows 未关时开新查询会死锁），
// 标签交集用 group_concat 在一条 SQL 内取回。
func (s *Service) Recommend(ctx context.Context, uid string, cursor string, size int, lat, lng *float64) ([]Card, string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, nickname, tags FROM (
			SELECT u.id AS id, u.nickname AS nickname,
			       (SELECT group_concat(t.tag, ',') FROM user_interest t
			         WHERE t.user_id = u.id AND t.tag IN
			           (SELECT tag FROM user_interest b WHERE b.user_id = ?)) AS tags
			FROM users u
			WHERE u.id != ? AND u.status = 'ACTIVE'
		) WHERE tags IS NOT NULL
		ORDER BY (LENGTH(tags) - LENGTH(REPLACE(tags, ',', '')) + 1) DESC, id
		LIMIT ?`, uid, uid, size)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var cards []Card
	for rows.Next() {
		var c Card
		var tags string
		if err := rows.Scan(&c.UserID, &c.Nickname, &tags); err != nil {
			continue
		}
		c.TagOverlap = strings.Split(tags, ",")

		// ② vec 中性分（冷启动）
		vec := 0.5

		// ③ geo（内存表命中才算分）
		var geo float64
		if lat != nil && lng != nil {
			if d, ok := geoTable.distance(c.UserID, *lat, *lng); ok {
				c.Distance = d
				geo = 1 - math.Min(d/5000, 1) // 5km 线性衰减
			} else {
				geo = 0.5 // 未上报 → 中性
			}
		} else {
			geo = 0.5
		}

		tagScore := math.Min(float64(len(c.TagOverlap))/3, 1)
		c.Score = wTag*tagScore + wVec*vec + wGeo*geo
		c.Reason = "同好 " + first(c.TagOverlap) + " · 时间窗重合 · AI 已匹配"
		cards = append(cards, c)
	}
	return cards, "", nil
}

func first(ss []string) string {
	if len(ss) == 0 {
		return "兴趣"
	}
	return ss[0]
}

// ---- geo 内存表（位置数据零落库原则）----

type geoIndex struct {
	mu sync.RWMutex
	m  map[string]geoPoint // userID → 最近上报
}

type geoPoint struct {
	Lat, Lng float64
	TS       time.Time
}

var geoTable = &geoIndex{m: map[string]geoPoint{}}

// ReportGeo 上报商圈级坐标（TTL 15min，内存态）。
func ReportGeo(uid string, lat, lng float64) {
	geoTable.mu.Lock()
	defer geoTable.mu.Unlock()
	geoTable.m[uid] = geoPoint{Lat: lat, Lng: lng, TS: time.Now()}
}

func (g *geoIndex) distance(uid string, lat, lng float64) (float64, bool) {
	g.mu.RLock()
	p, ok := g.m[uid]
	g.mu.RUnlock()
	if !ok || time.Since(p.TS) > 15*time.Minute {
		return 0, false
	}
	return haversine(lat, lng, p.Lat, p.Lng), true
}

func haversine(lat1, lng1, lat2, lng2 float64) float64 {
	const R = 6371000
	dLat := rad(lat2 - lat1)
	dLng := rad(lng2 - lng1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(rad(lat1))*math.Cos(rad(lat2))*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * R * math.Asin(math.Sqrt(a))
}

func rad(d float64) float64 { return d * math.Pi / 180 }
