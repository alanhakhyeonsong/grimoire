// Package index 는 SQLite FTS5 인덱스와 인덱서를 담당한다.
package index

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"

	"github.com/alanhakhyeonsong/grimoire/internal/frontmatter"
	_ "modernc.org/sqlite"
)

// DB 는 Grimoire 인덱스(재생성 가능한 파생 자산)다.
type DB struct{ sql *sql.DB }

// IndexEntry 는 get_index 결과 1건이다.
type IndexEntry struct {
	Path    string   `json:"path"`
	Title   string   `json:"title"`
	Type    string   `json:"type"`
	Domain  string   `json:"domain"`
	Tags    []string `json:"tags"`
	Date    string   `json:"date,omitempty"`
	Summary string   `json:"summary,omitempty"`
}

// SearchHit 는 search 결과 1건이다.
type SearchHit struct {
	Path  string `json:"path"`
	Title string `json:"title"`
	Type  string `json:"type"`
	Date  string `json:"date,omitempty"`
}

// TypeCount 는 type 별 집계다.
type TypeCount struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

func Open(indexDir string) (*DB, error) {
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		return nil, err
	}
	sdb, err := sql.Open("sqlite", filepath.Join(indexDir, "grimoire.db"))
	if err != nil {
		return nil, err
	}
	// 단일 연결로 직렬화: database/sql 의 연결 풀이 WAL 하에서 다중 연결로
	// 쓰기 경합 시 SQLITE_BUSY(database is locked)를 일으키는 것을 막는다.
	sdb.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
	} {
		if _, err := sdb.Exec(pragma); err != nil {
			return nil, err
		}
	}
	d := &DB{sql: sdb}
	if err := d.init(); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *DB) init() error {
	_, err := d.sql.Exec(`
		CREATE TABLE IF NOT EXISTS notes (
			path TEXT PRIMARY KEY, title TEXT, type TEXT, domain TEXT,
			tags TEXT, status TEXT, ai_access TEXT, date TEXT,
			summary TEXT, mtime INTEGER, inferred INTEGER
		);
		CREATE TABLE IF NOT EXISTS links (src TEXT, dst TEXT);
		CREATE VIRTUAL TABLE IF NOT EXISTS notes_fts USING fts5(
			path UNINDEXED, title, tags, body, tokenize='unicode61'
		);
	`)
	return err
}

func (d *DB) Clear() error {
	_, err := d.sql.Exec(`DELETE FROM notes; DELETE FROM links; DELETE FROM notes_fts;`)
	return err
}

func (d *DB) Upsert(n frontmatter.Note) error {
	if _, err := d.sql.Exec(
		`INSERT OR REPLACE INTO notes
		 (path,title,type,domain,tags,status,ai_access,date,summary,mtime,inferred)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		n.Path, n.Title, n.Type, n.Domain, strings.Join(n.Tags, ","),
		n.Status, n.AIAccess, n.Date, n.Summary, n.Mtime, boolToInt(n.Inferred),
	); err != nil {
		return err
	}
	if _, err := d.sql.Exec(
		`INSERT INTO notes_fts (path,title,tags,body) VALUES (?,?,?,?)`,
		n.Path, n.Title, strings.Join(n.Tags, " "), n.Body,
	); err != nil {
		return err
	}
	for _, dst := range n.Links {
		if _, err := d.sql.Exec(`INSERT INTO links (src,dst) VALUES (?,?)`, n.Path, dst); err != nil {
			return err
		}
	}
	return nil
}

// DeletePath 는 단일 노트의 인덱스 흔적(notes/notes_fts/links)을 제거한다.
// write_note 후 증분 갱신 시 FTS/links 중복 적재를 막기 위해 Upsert 전에 호출한다.
func (d *DB) DeletePath(path string) error {
	stmts := []struct {
		q   string
		arg string
	}{
		{`DELETE FROM notes WHERE path = ?`, path},
		{`DELETE FROM notes_fts WHERE path = ?`, path},
		{`DELETE FROM links WHERE src = ?`, path},
	}
	for _, s := range stmts {
		if _, err := d.sql.Exec(s.q, s.arg); err != nil {
			return err
		}
	}
	return nil
}

func splitTags(csv string) []string {
	if csv == "" {
		return []string{}
	}
	return strings.Split(csv, ",")
}

// PathMtimes 는 인덱스에 적재된 모든 노트의 path→mtime 맵을 반환한다.
// 증분 동기화(Sync)가 변경/삭제 노트를 판정하는 기준이다.
func (d *DB) PathMtimes() (map[string]int64, error) {
	rows, err := d.sql.Query(`SELECT path, mtime FROM notes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := make(map[string]int64)
	for rows.Next() {
		var p string
		var mt int64
		if err := rows.Scan(&p, &mt); err != nil {
			return nil, err
		}
		m[p] = mt
	}
	return m, rows.Err()
}

// scanEntries 는 (path,title,type,domain,tags,date,summary) 순 행을 IndexEntry 로 읽는다.
func scanEntries(rows *sql.Rows) ([]IndexEntry, error) {
	defer rows.Close()
	var out []IndexEntry
	for rows.Next() {
		var e IndexEntry
		var tags string
		if err := rows.Scan(&e.Path, &e.Title, &e.Type, &e.Domain, &tags, &e.Date, &e.Summary); err != nil {
			return nil, err
		}
		e.Tags = splitTags(tags)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Runbooks 는 type='runbook' 노트를 반환한다(name 지정 시 title/path/tags LIKE 필터).
func (d *DB) Runbooks(name string, limit int) ([]IndexEntry, error) {
	q := `SELECT path,title,type,domain,tags,date,summary FROM notes WHERE type = 'runbook'`
	var args []any
	if name != "" {
		q += ` AND (title LIKE ? OR path LIKE ? OR tags LIKE ?)`
		like := "%" + name + "%"
		args = append(args, like, like, like)
	}
	q += ` ORDER BY date DESC, path LIMIT ?`
	args = append(args, limit)
	rows, err := d.sql.Query(q, args...)
	if err != nil {
		return nil, err
	}
	return scanEntries(rows)
}

// ContextCandidates 는 project 어휘로 관련 런북·노트 후보를 선별한다.
// project 가 비면 런북은 최근순 전체, 일반 노트는 빈 결과를 반환한다.
func (d *DB) ContextCandidates(project string, limit int) (runbooks, notes []IndexEntry, err error) {
	like := "%" + project + "%"
	const cols = `SELECT path,title,type,domain,tags,date,summary FROM notes`

	rbQ := cols + ` WHERE type = 'runbook'`
	var rbArgs []any
	if project != "" {
		rbQ += ` AND (tags LIKE ? OR title LIKE ? OR path LIKE ?)`
		rbArgs = append(rbArgs, like, like, like)
	}
	rbQ += ` ORDER BY date DESC, path LIMIT ?`
	rbArgs = append(rbArgs, limit)
	rbRows, err := d.sql.Query(rbQ, rbArgs...)
	if err != nil {
		return nil, nil, err
	}
	if runbooks, err = scanEntries(rbRows); err != nil {
		return nil, nil, err
	}

	if project == "" {
		return runbooks, []IndexEntry{}, nil
	}
	nRows, err := d.sql.Query(
		cols+` WHERE type != 'runbook' AND (tags LIKE ? OR title LIKE ? OR path LIKE ?)
		       ORDER BY date DESC, path LIMIT ?`,
		like, like, like, limit)
	if err != nil {
		return nil, nil, err
	}
	if notes, err = scanEntries(nRows); err != nil {
		return nil, nil, err
	}
	return runbooks, notes, nil
}

// GetIndex 는 필터(type/domain/dir prefix)로 목차를 반환한다.
func (d *DB) GetIndex(typ, domain, dir string, limit int) ([]IndexEntry, error) {
	q := `SELECT path,title,type,domain,tags,date,summary FROM notes WHERE 1=1`
	var args []any
	if typ != "" {
		q += ` AND type = ?`
		args = append(args, typ)
	}
	if domain != "" {
		q += ` AND domain = ?`
		args = append(args, domain)
	}
	if dir != "" {
		q += ` AND path LIKE ?`
		args = append(args, dir+"/%")
	}
	q += ` ORDER BY date DESC, path LIMIT ?`
	args = append(args, limit)

	rows, err := d.sql.Query(q, args...)
	if err != nil {
		return nil, err
	}
	return scanEntries(rows)
}

// sanitizeFTSQuery 는 사용자 입력을 안전한 FTS5 MATCH 식으로 변환한다.
// 각 공백 구분 토큰을 큰따옴표로 감싼 phrase 로 처리해(내부 " 는 이중화)
// *, :, -, ^, (), AND/OR/NOT 등 FTS5 문법 특수문자의 의미를 무력화한다.
// 토큰들은 공백으로 join 되어 FTS5 의 암묵 AND 로 결합된다.
// SQL 인젝션은 파라미터 바인딩(?)이 막고, 본 함수는 FTS5 문법 에러를 막는다.
func sanitizeFTSQuery(query string) string {
	fields := strings.Fields(query)
	quoted := make([]string, 0, len(fields))
	for _, f := range fields {
		quoted = append(quoted, `"`+strings.ReplaceAll(f, `"`, `""`)+`"`)
	}
	return strings.Join(quoted, " ")
}

// Search 는 FTS5 검색을 수행한다(tags/dir 추가 필터).
func (d *DB) Search(query string, tags []string, dir string, limit int) ([]SearchHit, error) {
	match := sanitizeFTSQuery(query)
	if match == "" {
		return nil, nil // 유효 토큰 없음(공백/특수문자뿐) → 빈 결과
	}
	q := `SELECT n.path,n.title,n.type,n.date
	      FROM notes_fts f JOIN notes n ON n.path = f.path
	      WHERE notes_fts MATCH ?`
	args := []any{match}
	for _, t := range tags {
		q += ` AND n.tags LIKE ?`
		args = append(args, "%"+t+"%")
	}
	if dir != "" {
		q += ` AND n.path LIKE ?`
		args = append(args, dir+"/%")
	}
	q += ` ORDER BY rank LIMIT ?`
	args = append(args, limit)

	rows, err := d.sql.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SearchHit
	for rows.Next() {
		var h SearchHit
		if err := rows.Scan(&h.Path, &h.Title, &h.Type, &h.Date); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// LinksOf 는 outgoing(이 노트가 가리키는)과 incoming(이 노트를 가리키는) 링크를 반환한다.
func (d *DB) LinksOf(path string) (outgoing, incoming []string, err error) {
	base := strings.TrimSuffix(filepath.Base(path), ".md")

	outRows, err := d.sql.Query(`SELECT dst FROM links WHERE src = ?`, path)
	if err != nil {
		return nil, nil, err
	}
	defer outRows.Close()
	outgoing = []string{}
	for outRows.Next() {
		var s string
		if err := outRows.Scan(&s); err != nil {
			return nil, nil, err
		}
		outgoing = append(outgoing, s)
	}

	inRows, err := d.sql.Query(`SELECT DISTINCT src FROM links WHERE dst = ? OR dst = ?`, base, path)
	if err != nil {
		return nil, nil, err
	}
	defer inRows.Close()
	incoming = []string{}
	for inRows.Next() {
		var s string
		if err := inRows.Scan(&s); err != nil {
			return nil, nil, err
		}
		incoming = append(incoming, s)
	}
	return outgoing, incoming, nil
}

func (d *DB) CountUnder(prefix string) int {
	var c int
	_ = d.sql.QueryRow(`SELECT COUNT(*) FROM notes WHERE path LIKE ?`, prefix+"%").Scan(&c)
	return c
}

func (d *DB) Total() int {
	var c int
	_ = d.sql.QueryRow(`SELECT COUNT(*) FROM notes`).Scan(&c)
	return c
}

func (d *DB) LinkCount() int {
	var c int
	_ = d.sql.QueryRow(`SELECT COUNT(*) FROM links`).Scan(&c)
	return c
}

func (d *DB) TypeCounts() ([]TypeCount, error) {
	rows, err := d.sql.Query(`SELECT type, COUNT(*) FROM notes GROUP BY type ORDER BY COUNT(*) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TypeCount
	for rows.Next() {
		var t TypeCount
		if err := rows.Scan(&t.Type, &t.Count); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (d *DB) Close() error { return d.sql.Close() }

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
