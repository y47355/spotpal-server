-- 002_seed.sql · make seed 演示数据（3 用户 / 5 兴趣 / 1 搭局 + 2 轮协商）
-- 幂等：REPLACE INTO，重复执行安全

REPLACE INTO users (id, phone, nickname, city, credit_score) VALUES
  ('u_demo_ye',  '13800000001', '阿叶', '上海', 690),
  ('u_demo_zhe', '13800000002', '阿哲', '上海', 705),
  ('u_demo_lu',  '13800000003', '小鹿', '上海', 645);

REPLACE INTO user_interest (user_id, tag, weight) VALUES
  ('u_demo_ye',  '羽毛球', 1.0),
  ('u_demo_ye',  '艺术展', 0.8),
  ('u_demo_ye',  'citywalk', 0.6),
  ('u_demo_zhe', '羽毛球', 1.0),
  ('u_demo_zhe', '电影', 0.7),
  ('u_demo_lu',  '艺术展', 1.0);

REPLACE INTO user_pref (user_id, cost_mode, time_slots, role_pref) VALUES
  ('u_demo_ye',  'AA', '{"weekdays":["FRI"],"hours":[20,21,22]}', 'ANY'),
  ('u_demo_zhe', 'AA', '{"weekdays":["FRI"],"hours":[19,20,21,22]}', 'ANY'),
  ('u_demo_lu',  'ROTATE', '{"weekdays":["SAT"],"hours":[13,14,15]}', 'ANY');

REPLACE INTO squads (id, initiator_id, status, activity, time_window, cost_mode, expire_at) VALUES
  ('sq_demo_1', 'u_demo_ye', 'NEGOTIATING', '羽毛球',
   '{"start":"2026-09-12T20:00:00Z","end":"2026-09-12T22:00:00Z"}', 'AA',
   '2026-09-11T20:00:00Z');

REPLACE INTO candidate_pool (id, user_id, candidate_id, intent_snapshot, status) VALUES
  ('cp_demo_1', 'u_demo_ye', 'u_demo_zhe',
   '{"activity":"羽毛球","time":"FRI 20-22","cost":"AA","role":"ANY"}', 'WAITING');

REPLACE INTO negotiations (id, squad_id, candidate_id, round, status, expire_at) VALUES
  ('ngt_demo_1', 'sq_demo_1', 'u_demo_zhe', 2, 'ACTIVE', '2026-09-11T20:00:00Z');

REPLACE INTO negotiation_rounds (id, negotiation_id, round_no, proposal, feedback) VALUES
  ('nr_demo_1', 'ngt_demo_1', 1,
   '{"time":"FRI 20:00-22:00","venue":"徐汇滨江羽毛球馆","cost_mode":"AA","per_head":42}',
   '{"from":"u_demo_ye","ok":true,"note":"时间可以"}'),
  ('nr_demo_2', 'ngt_demo_1', 2,
   '{"time":"FRI 20:00-22:00","venue":"徐汇滨江羽毛球馆 3 号场","cost_mode":"AA","per_head":42}',
   NULL);
