package watch

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

//go:generate true

// Server 是只监听回环地址的看板服务。
//
// 刻意不做鉴权：它只 bind 127.0.0.1，且不暴露任何写操作 ——
// 能连上它的人已经是本机用户，本来就能直接读 ~/.ith5/trace/。
// 反过来说，**绝不能把监听地址放开到 0.0.0.0**：trace 里有 task 描述。
type Server struct {
	state      *State
	projectDir string

	mu      sync.Mutex
	clients map[chan []byte]struct{}
}

func NewServer(st *State, projectDir string) *Server {
	return &Server{state: st, projectDir: projectDir, clients: map[chan []byte]struct{}{}}
}

// payload 是推给前端的一帧。
type payload struct {
	Snapshot
	Features []Feature `json:"features"`
}

func (s *Server) frame() []byte {
	p := payload{Snapshot: s.state.Snapshot(), Features: ReadFeatures(s.projectDir)}
	b, err := json.Marshal(p)
	if err != nil {
		return nil
	}
	return b
}

// Broadcast 把当前视图推给所有连着的浏览器。
func (s *Server) Broadcast() {
	b := s.frame()
	if b == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.clients {
		// 非阻塞发送：某个浏览器卡住不能拖慢 tailer。
		select {
		case ch <- b:
		default:
		}
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/events", s.handleSSE)
	return mux
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(dashboardHTML)
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(s.frame())
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := make(chan []byte, 8)
	s.mu.Lock()
	s.clients[ch] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.clients, ch)
		s.mu.Unlock()
	}()

	// 连上先给一帧完整快照，否则新开的页面要等到下一次事件才有内容。
	if b := s.frame(); b != nil {
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}

	// 心跳兼作「运行中区间的已耗时」刷新源：没有新事件时，
	// 页面上正在跑的那条也应该继续走秒。
	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case b := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		case <-tick.C:
			if b := s.frame(); b != nil {
				fmt.Fprintf(w, "data: %s\n\n", b)
				flusher.Flush()
			}
		}
	}
}

// Serve 起服务并阻塞，直到 ctx 取消。返回实际监听地址供调用方打开浏览器。
//
// 端口传 0 时由内核分配，避免与用户其它服务撞port；调用方从返回的
// addr 里取真实端口。
func Serve(ctx context.Context, addr string, s *Server, ready func(string)) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	if ready != nil {
		ready(ln.Addr().String())
	}
	errCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	}
}
