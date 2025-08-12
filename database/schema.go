package database

const schema = `
CREATE TABLE IF NOT EXISTS categories (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL UNIQUE,
	sort_order INTEGER DEFAULT 0
);
CREATE TABLE IF NOT EXISTS boards (
	id TEXT PRIMARY KEY, name TEXT NOT NULL, description TEXT,
	max_threads INTEGER DEFAULT 100, bump_limit INTEGER DEFAULT 500,
	image_required BOOLEAN DEFAULT 0, password TEXT DEFAULT '',
	require_pass BOOLEAN DEFAULT 0, color_scheme TEXT DEFAULT 'yalie-blue',
	created DATETIME, archived BOOLEAN DEFAULT 0,
	category_id INTEGER DEFAULT 1,
	sort_order INTEGER DEFAULT 0,
	FOREIGN KEY (category_id) REFERENCES categories(id)
);
CREATE TABLE IF NOT EXISTS threads (
	id INTEGER PRIMARY KEY AUTOINCREMENT, board_id TEXT, subject TEXT,
	bump DATETIME, reply_count INTEGER DEFAULT 0, image_count INTEGER DEFAULT 0,
	locked BOOLEAN DEFAULT 0, sticky BOOLEAN DEFAULT 0, archived BOOLEAN DEFAULT 0,
	FOREIGN KEY (board_id) REFERENCES boards(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS posts (
	id INTEGER PRIMARY KEY AUTOINCREMENT, board_id TEXT, thread_id INTEGER,
	name TEXT, tripcode TEXT, content TEXT, image_path TEXT, image_hash TEXT,
	timestamp DATETIME, ip_hash TEXT, 
	cookie_hash TEXT, -- To store the persistent cookie hash
	deletion_password TEXT,
	FOREIGN KEY (board_id) REFERENCES boards(id) ON DELETE CASCADE,
	FOREIGN KEY (thread_id) REFERENCES threads(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS backlinks (
	source_post_id INTEGER NOT NULL,
	target_post_id INTEGER NOT NULL,
	PRIMARY KEY (source_post_id, target_post_id),
	FOREIGN KEY (source_post_id) REFERENCES posts(id) ON DELETE CASCADE,
	FOREIGN KEY (target_post_id) REFERENCES posts(id) ON DELETE CASCADE
);
-- The bans table is generic to handle different hash types
CREATE TABLE IF NOT EXISTS bans (
	id INTEGER PRIMARY KEY AUTOINCREMENT, 
	hash TEXT NOT NULL,
	ban_type TEXT NOT NULL, -- 'ip' or 'cookie'
	reason TEXT, 
	created_at DATETIME, 
	expires_at DATETIME
);
CREATE TABLE IF NOT EXISTS reports (
	id INTEGER PRIMARY KEY AUTOINCREMENT, post_id INTEGER, reason TEXT,
	ip_hash TEXT, created_at DATETIME, resolved BOOLEAN DEFAULT 0
);
-- New table for moderator action logging
CREATE TABLE IF NOT EXISTS mod_actions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp DATETIME NOT NULL,
    moderator_hash TEXT NOT NULL,
    action TEXT NOT NULL,
    target_id INTEGER,
    details TEXT
);
-- New FTS5 table for fast searching
CREATE VIRTUAL TABLE IF NOT EXISTS posts_fts USING fts5(
    subject,
    content,
    content='posts',
    content_rowid='id'
);
-- Triggers to keep the FTS table synchronized with the posts table
CREATE TRIGGER IF NOT EXISTS posts_ai AFTER INSERT ON posts BEGIN
  INSERT INTO posts_fts(rowid, subject, content) VALUES (
      new.id, 
      (SELECT subject FROM threads WHERE id = new.thread_id), 
      new.content
  );
END;
CREATE TRIGGER IF NOT EXISTS posts_ad AFTER DELETE ON posts BEGIN
  INSERT INTO posts_fts(posts_fts, rowid, subject, content) VALUES (
      'delete',
      old.id,
      (SELECT subject FROM threads WHERE id = old.thread_id),
      old.content
  );
END;
CREATE TRIGGER IF NOT EXISTS posts_au AFTER UPDATE ON posts BEGIN
  INSERT INTO posts_fts(posts_fts, rowid, subject, content) VALUES ('delete', old.id, (SELECT subject FROM threads WHERE id = old.thread_id), old.content);
  INSERT INTO posts_fts(rowid, subject, content) VALUES (new.id, (SELECT subject FROM threads WHERE id = new.thread_id), new.content);
END;


-- --- INDEXES ---
CREATE INDEX IF NOT EXISTS idx_backlinks_target ON backlinks(target_post_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_bans_hash_type ON bans(hash, ban_type); -- Ensures no duplicate bans
CREATE INDEX IF NOT EXISTS idx_posts_thread ON posts(thread_id);
CREATE INDEX IF NOT EXISTS idx_threads_board_bump ON threads(board_id, archived, sticky DESC, bump DESC);
CREATE INDEX IF NOT EXISTS idx_posts_ip_hash ON posts(ip_hash);
CREATE INDEX IF NOT EXISTS idx_posts_cookie_hash ON posts(cookie_hash);
CREATE INDEX IF NOT EXISTS idx_posts_image_hash ON posts(image_hash);
CREATE INDEX IF NOT EXISTS idx_reports_post_id ON reports(post_id);
CREATE INDEX IF NOT EXISTS idx_mod_actions_time ON mod_actions(timestamp DESC);
`
