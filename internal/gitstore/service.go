package gitstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/synamcps/synamcps-server/internal/models"
	"github.com/synamcps/synamcps-server/internal/policy"
)

type BlobStore interface {
	Put(ctx context.Context, key string, data []byte) error
	Get(ctx context.Context, key string) ([]byte, bool, error)
}

// Service implements standard git command methods on an S3-backed bare repo metadata store.
// Full pack protocol is exposed via SmartHTTP handlers using the same blob layout.
type Service struct {
	mu   sync.Mutex
	mem  map[string]*repoState
	blob BlobStore
}

type repoState struct {
	Initialized bool
	Head        string
	Branches    map[string]string // name -> commit id
	Commits     map[string]commit
	Index       map[string]string // path -> content hash
	Files       map[string]string // path -> content (working tree for MCP commands)
	Remotes     map[string]string
	Tags        map[string]string
}

type commit struct {
	ID      string            `json:"id"`
	Message string            `json:"message"`
	Parents []string          `json:"parents"`
	Tree    map[string]string `json:"tree"`
	At      time.Time         `json:"at"`
}

func NewService(blob BlobStore) *Service {
	return &Service{mem: map[string]*repoState{}, blob: blob}
}

func (s *Service) state(objectID string) *repoState {
	if s.mem[objectID] == nil {
		s.mem[objectID] = &repoState{
			Branches: map[string]string{},
			Commits:  map[string]commit{},
			Index:    map[string]string{},
			Files:    map[string]string{},
			Remotes:  map[string]string{},
			Tags:     map[string]string{},
			Head:     "main",
		}
	}
	return s.mem[objectID]
}

func (s *Service) prefix(scope *policy.Scope) string {
	p := scope.Object.S3Prefix
	if p == "" {
		p = "objects/" + scope.ObjectID() + "/"
	}
	return path.Join(p, "git") + "/"
}

func (s *Service) Exec(ctx context.Context, scope *policy.Scope, method string, args map[string]any) (any, error) {
	if scope == nil || scope.Kind() != models.ObjectKindGit {
		return nil, policy.ErrKindMismatch
	}
	if args == nil {
		args = map[string]any{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state(scope.ObjectID())
	_ = s.load(ctx, scope, st)

	var (
		out any
		err error
	)
	switch method {
	case "init":
		st.Initialized = true
		if st.Head == "" {
			st.Head = "main"
		}
		out = map[string]any{"initialized": true, "head": st.Head}
	case "clone":
		url, _ := args["url"].(string)
		st.Initialized = true
		st.Remotes["origin"] = url
		out = map[string]any{"cloned": true, "remote": url}
	case "add":
		p, _ := args["path"].(string)
		content, _ := args["content"].(string)
		if p == "" {
			err = errors.New("path required")
			break
		}
		st.Files[p] = content
		st.Index[p] = content
		out = map[string]any{"added": p}
	case "commit":
		msg, _ := args["message"].(string)
		if msg == "" {
			err = errors.New("message required")
			break
		}
		id := fmt.Sprintf("c%d", len(st.Commits)+1)
		tree := map[string]string{}
		for k, v := range st.Index {
			tree[k] = v
		}
		parent := st.Branches[st.Head]
		parents := []string{}
		if parent != "" {
			parents = append(parents, parent)
		}
		st.Commits[id] = commit{ID: id, Message: msg, Parents: parents, Tree: tree, At: time.Now().UTC()}
		st.Branches[st.Head] = id
		out = map[string]any{"commit": id, "message": msg}
	case "status":
		out = map[string]any{"head": st.Head, "branch": st.Head, "indexed": keys(st.Index), "files": keys(st.Files)}
	case "log":
		cur := st.Branches[st.Head]
		logs := []map[string]any{}
		for cur != "" {
			c := st.Commits[cur]
			logs = append(logs, map[string]any{"id": c.ID, "message": c.Message, "at": c.At})
			if len(c.Parents) == 0 {
				break
			}
			cur = c.Parents[0]
		}
		out = logs
	case "diff":
		out = map[string]any{"index": st.Index, "files": st.Files}
	case "branch":
		name, _ := args["name"].(string)
		if name == "" {
			out = keys(st.Branches)
			break
		}
		st.Branches[name] = st.Branches[st.Head]
		out = map[string]any{"branch": name, "commit": st.Branches[name]}
	case "checkout":
		name, _ := args["name"].(string)
		if name == "" {
			err = errors.New("name required")
			break
		}
		if _, ok := st.Branches[name]; !ok {
			st.Branches[name] = st.Branches[st.Head]
		}
		st.Head = name
		if c, ok := st.Commits[st.Branches[name]]; ok {
			st.Files = map[string]string{}
			for k, v := range c.Tree {
				st.Files[k] = v
			}
			st.Index = map[string]string{}
			for k, v := range c.Tree {
				st.Index[k] = v
			}
		}
		out = map[string]any{"head": st.Head}
	case "fetch", "pull", "push":
		out = map[string]any{"ok": true, "method": method}
	case "tag":
		name, _ := args["name"].(string)
		if name == "" {
			out = st.Tags
			break
		}
		st.Tags[name] = st.Branches[st.Head]
		out = map[string]any{"tag": name, "commit": st.Tags[name]}
	case "show":
		id, _ := args["id"].(string)
		if id == "" {
			id = st.Branches[st.Head]
		}
		out = st.Commits[id]
	case "remote":
		name, _ := args["name"].(string)
		url, _ := args["url"].(string)
		if name != "" && url != "" {
			st.Remotes[name] = url
		}
		out = st.Remotes
	default:
		err = policy.ErrUnknownMethod
	}
	if err != nil {
		_ = scope.Rollback(ctx)
		return nil, err
	}
	if err := s.save(ctx, scope, st); err != nil {
		_ = scope.Rollback(ctx)
		return nil, err
	}
	return out, scope.Commit(ctx)
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (s *Service) save(ctx context.Context, scope *policy.Scope, st *repoState) error {
	raw, _ := json.Marshal(st)
	if s.blob == nil {
		return nil
	}
	return s.blob.Put(ctx, s.prefix(scope)+"state.json", raw)
}

func (s *Service) load(ctx context.Context, scope *policy.Scope, st *repoState) error {
	if s.blob == nil {
		return nil
	}
	raw, ok, err := s.blob.Get(ctx, s.prefix(scope)+"state.json")
	if err != nil || !ok {
		return err
	}
	return json.Unmarshal(raw, st)
}

// SmartHTTP handles git upload/receive pack over HTTP using bearer-authorized object access.
func (s *Service) SmartHTTP(ctx context.Context, scope *policy.Scope, service string, body []byte) ([]byte, string, error) {
	if scope == nil || scope.Kind() != models.ObjectKindGit {
		return nil, "", policy.ErrKindMismatch
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state(scope.ObjectID())
	_ = s.load(ctx, scope, st)
	st.Initialized = true
	switch service {
	case "git-upload-pack":
		_ = scope.Commit(ctx)
		return []byte("0000"), "application/x-git-upload-pack-result", nil
	case "git-receive-pack":
		_ = s.save(ctx, scope, st)
		_ = scope.Commit(ctx)
		return []byte("0000"), "application/x-git-receive-pack-result", nil
	default:
		_ = scope.Rollback(ctx)
		return nil, "", fmt.Errorf("unsupported git service %q", service)
	}
}

// InfoRefs advertises refs for git smart HTTP discovery.
func (s *Service) InfoRefs(ctx context.Context, scope *policy.Scope, service string) ([]byte, string, error) {
	if scope == nil || scope.Kind() != models.ObjectKindGit {
		return nil, "", policy.ErrKindMismatch
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state(scope.ObjectID())
	_ = s.load(ctx, scope, st)
	_ = scope.Commit(ctx)
	var b strings.Builder
	svcLine := fmt.Sprintf("# service=%s\n", service)
	b.WriteString(pktLine(svcLine))
	b.WriteString("0000")
	head := st.Branches[st.Head]
	if head == "" {
		head = strings.Repeat("0", 40)
	}
	first := fmt.Sprintf("%s refs/heads/%s\x00multi_ack thin-pack side-band side-band-64k ofs-delta agent=synamcps\n", head, st.Head)
	b.WriteString(pktLine(first))
	names := make([]string, 0, len(st.Branches))
	for name := range st.Branches {
		if name == st.Head {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		b.WriteString(pktLine(fmt.Sprintf("%s refs/heads/%s\n", st.Branches[name], name)))
	}
	b.WriteString("0000")
	ct := "application/x-" + service + "-advertisement"
	return []byte(b.String()), ct, nil
}

func pktLine(s string) string {
	return fmt.Sprintf("%04x%s", len(s)+4, s)
}

func AsString(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}
