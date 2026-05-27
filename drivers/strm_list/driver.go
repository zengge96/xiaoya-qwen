package strm_list

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path" // 使用标准 path 处理斜杠
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/model"
	"github.com/alist-org/alist/v3/internal/op"
	// 导入SQLite3驱动
	_ "github.com/mattn/go-sqlite3"
	log "github.com/sirupsen/logrus"
)

// Addition 驱动配置项
type Addition struct {
	// 关键：移除 driver.RootPath，由驱动完全控制路径解析
	TxtPath string `json:"txt_path" required:"true" help:"strm.txt 的绝对路径"`
	DbPath  string `json:"db_path" required:"true" help:"SQLite 数据库文件的绝对路径"`
}

type StrmList struct {
	model.Storage
	Addition
	db *sql.DB
}

func (d *StrmList) Config() driver.Config {
	return driver.Config{
		Name:        "StrmList",
		OnlyLocal:   true,
		LocalSort:   true,
		NoCache:     true,
		DefaultRoot: "/",
	}
}

func (d *StrmList) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *StrmList) Init(ctx context.Context) error {
	if d.DbPath == "" || d.TxtPath == "" {
		return fmt.Errorf("strm.txt路径和数据库路径不能为空")
	}

	dir := filepath.Dir(d.DbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建DB目录失败: %v", err)
	}

	if _, err := os.Stat(d.DbPath); err == nil {
		log.Infof("[StrmList] 检测到旧数据库，正在清理以重新初始化...")
		_ = os.Remove(d.DbPath)
	}

	dsn := d.DbPath + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=cache_size=-10000"
	var err error
	d.db, err = sql.Open("sqlite3", dsn)
	if err != nil {
		return fmt.Errorf("打开SQLite失败: %v", err)
	}

	if err := d.db.Ping(); err != nil {
		d.db.Close()
		return fmt.Errorf("SQLite连接测试失败: %v", err)
	}
	d.db.SetMaxOpenConns(1)

	createTableSQL := `
	CREATE TABLE IF NOT EXISTS nodes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		parent_id INTEGER NOT NULL DEFAULT 0,
		is_dir BOOLEAN NOT NULL DEFAULT 0,
		content TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX IF NOT EXISTS idx_parent_name ON nodes(parent_id, name);
	`
	if _, err = d.db.Exec(createTableSQL); err != nil {
		return err
	}

	var count int
	_ = d.db.QueryRow("SELECT COUNT(*) FROM nodes").Scan(&count)
	if count <= 1 {
		go d.importTxtTask()
	}
	return nil
}

func (d *StrmList) Drop(ctx context.Context) error {
	if d.db != nil {
		return d.db.Close()
	}
	return nil
}

// List 列目录
func (d *StrmList) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	// AList 会传入当前对象的路径
	p := dir.GetPath()
	nodeID, _, _, err := d.findNodeByPath(p)
	if err != nil {
		log.Errorf("[StrmList] List路径解析失败 [%s]: %v", p, err)
		return nil, err
	}

	rows, err := d.db.Query(
		"SELECT name, is_dir, length(cast(content as blob)) FROM nodes WHERE parent_id = ?",
		nodeID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var objs []model.Obj
	now := time.Now()
	for rows.Next() {
		var name string
		var isDir bool
		var contentByteLen int64
		
		if err := rows.Scan(&name, &isDir, &contentByteLen); err != nil {
			continue
		}
		
		// 3. 构建 AList 对象
		//末尾加80个padding字符，nginx反代替换逻辑会保证content长度不变
		size := contentByteLen + 80;
		if isDir {
			size = 0
		}

		objs = append(objs, &model.Object{
			Name:     name,
			Size:     size,
			Modified: now,
			IsFolder: isDir,
			Path:     path.Join(p, name), 
		})
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return objs, nil
}

// Get 获取文件详情
func (d *StrmList) Get(ctx context.Context, pathStr string) (model.Obj, error) {
	_, isDir, content, err := d.findNodeByPath(pathStr)
	if err != nil {
		return nil, err
	}
	return &model.Object{
		Name:     path.Base(pathStr),
		Size:     int64(len([]byte(content))),
		Modified: time.Now(),
		IsFolder: isDir,
		Path:     pathStr,
	}, nil
}

func (d *StrmList) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	_, _, content, err := d.findNodeByPath(file.GetPath())
	if err != nil {
		return nil, err
	}

	data := []byte(content)
	return &model.Link{
		Data: io.NopCloser(bytes.NewReader(data)),
		Header: http.Header{
			"Content-Type": []string{"text/plain; charset=utf-8"},
			"Content-Length": []string{strconv.Itoa(len(data))},
		},
	}, nil
}

// 内部辅助：路径转节点ID - 逐级解析，核心逻辑无修改
func (d *StrmList) findNodeByPath(path string) (id int64, isDir bool, content string, err error) {
	path = strings.Trim(path, "/")
	if path == "" {
		return 0, true, "", nil // 根目录固定ID=0
	}
	parts := strings.Split(path, "/")
	var currentParent int64 = 0
	for _, part := range parts {
		err = d.db.QueryRow(
			"SELECT id, is_dir, content FROM nodes WHERE parent_id = ? AND name = ?",
			currentParent, part,
		).Scan(&id, &isDir, &content)
		if err != nil {
			return 0, false, "", fmt.Errorf("路径段[%s]查询失败: %v", part, err)
		}
		currentParent = id
	}

	//末尾加80个padding字符，nginx反代替换逻辑会保证content长度不变
	content = content + "################################################################################" 

	return id, isDir, content, nil
}

// 异步导入strm.txt - 保留原高效逻辑，事务+预编译+目录缓存，增加错误日志
func (d *StrmList) importTxtTask() {
	log.Infof("[StrmList] 开始从 %s 导入数据", d.TxtPath)
	// 检查strm.txt文件是否存在
	if _, err := os.Stat(d.TxtPath); os.IsNotExist(err) {
		log.Errorf("[StrmList] strm.txt文件不存在: %s", d.TxtPath)
		return
	}
	file, err := os.Open(d.TxtPath)
	if err != nil {
		log.Errorf("[StrmList] 打开strm.txt失败: %v", err)
		return
	}
	defer file.Close()

	// 事务提升导入效率
	tx, err := d.db.Begin()
	if err != nil {
		log.Errorf("[StrmList] 开启事务失败: %v", err)
		return
	}
	// 初始化根节点（ID=0）
	_, _ = tx.Exec("INSERT OR IGNORE INTO nodes (id, name, parent_id, is_dir) VALUES (0, '', -1, 1)")
	// 预编译插入语句，提升批量导入速度
	stmt, err := tx.Prepare("INSERT INTO nodes (name, parent_id, is_dir, content) VALUES (?, ?, ?, ?)")
	if err != nil {
		log.Errorf("[StrmList] 预编译语句失败: %v", err)
		_ = tx.Rollback()
		return
	}
	defer stmt.Close()

	dirCache := map[string]int64{"": 0} // 目录缓存避免重复创建
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024) // 大缓冲区支持超长行

	count := 0
	skipCount := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			skipCount++
			continue
		}
		// 按#分割路径和内容，格式：路径#strm_URL（严格校验格式）
		parts := strings.SplitN(line, "#", 2)
		if len(parts) < 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			log.Warnf("[StrmList] 行格式错误，跳过: %s", line)
			skipCount++
			continue
		}
		rawPath, content := strings.Trim(parts[0], "/"), strings.TrimSpace(parts[1])
		pathParts := strings.Split(rawPath, "/")
		if len(pathParts) == 0 {
			skipCount++
			continue
		}

		// 逐级创建目录并缓存
		var currParent int64 = 0
		currPathAcc := ""
		for _, part := range pathParts[:len(pathParts)-1] {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if currPathAcc == "" {
				currPathAcc = part
			} else {
				currPathAcc += "/" + part
			}
			// 缓存命中，直接使用父节点ID
			if id, ok := dirCache[currPathAcc]; ok {
				currParent = id
				continue
			}
			// 缓存未命中，插入目录节点
			res, err := tx.Exec("INSERT INTO nodes (name, parent_id, is_dir) VALUES (?, ?, 1)", part, currParent)
			if err != nil {
				log.Warnf("[StrmList] 创建目录[%s]失败: %v", currPathAcc, err)
				continue
			}
			// 获取新插入目录的ID，加入缓存
			dirID, err := res.LastInsertId()
			if err != nil {
				log.Warnf("[StrmList] 获取目录ID失败: %v", err)
				continue
			}
			currParent = dirID
			dirCache[currPathAcc] = dirID
		}
		// 插入strm文件节点（最后一个路径段为文件名）
		filename := strings.TrimSpace(pathParts[len(pathParts)-1])
		if filename == "" {
			skipCount++
			continue
		}
		if _, err := stmt.Exec(filename, currParent, 0, content); err == nil {
			count++
		} else {
			log.Warnf("[StrmList] 插入文件[%s]失败: %v", rawPath, err)
			skipCount++
		}
	}

	// 检查扫描错误并提交/回滚事务
	if err := scanner.Err(); err != nil {
		log.Errorf("[StrmList] 扫描strm.txt文件失败: %v", err)
		_ = tx.Rollback()
		return
	}
	if err := tx.Commit(); err != nil {
		log.Errorf("[StrmList] 提交导入事务失败: %v", err)
		_ = tx.Rollback()
		return
	}
	log.Infof("[StrmList] 导入完成！成功导入%d条，跳过%d条", count, skipCount)
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &StrmList{}
	})
}
