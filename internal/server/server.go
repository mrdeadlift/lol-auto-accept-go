package server

import (
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/gorilla/mux"
	"lol-auto-accept/internal/app"
)

type Server struct {
	app *app.App
}

func NewServer(app *app.App) *Server {
	return &Server{
		app: app,
	}
}

func (s *Server) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.app.GetWebSocketManager().HandleConnection(w, r)
	if err != nil {
		return
	}
	defer conn.Close()

	// 接続時に現在の状態を送信
	if s.app.IsRunning() {
		s.app.GetWebSocketManager().UpdateStatus("監視中...")
	} else if s.app.IsAutoWatching() {
		s.app.GetWebSocketManager().UpdateStatus("自動監視中...")
	} else {
		s.app.GetWebSocketManager().UpdateStatus("停止中")
	}

	defer func() {
		s.app.GetWebSocketManager().RemoveConnection(conn)
	}()

	for {
		var msg map[string]interface{}
		err := conn.ReadJSON(&msg)
		if err != nil {
			break
		}

		action := msg["action"].(string)
		switch action {
		case "start":
			s.app.StartMonitoring()
		case "stop":
			s.app.StopMonitoring()
		case "test":
			s.app.TestEnvironment()
		}
	}
}

func (s *Server) ServeHTML(w http.ResponseWriter, r *http.Request) {
	htmlContent := `
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>LoL Auto Accept</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 20px; background-color: #f5f5f5; }
        .container { max-width: 600px; margin: 0 auto; background: white; padding: 20px; border-radius: 8px; box-shadow: 0 2px 10px rgba(0,0,0,0.1); }
        h1 { color: #333; text-align: center; margin-bottom: 30px; }
        .status { padding: 10px; margin: 10px 0; border-radius: 4px; font-weight: bold; text-align: center; }
        .status.stopped { background-color: #fee; color: #c33; }
        .status.running { background-color: #efe; color: #3c3; }
        .buttons { text-align: center; margin: 20px 0; }
        button { padding: 10px 20px; margin: 0 5px; border: none; border-radius: 4px; cursor: pointer; font-size: 14px; }
        .start { background-color: #4CAF50; color: white; }
        .stop { background-color: #f44336; color: white; }
        .test { background-color: #2196F3; color: white; }
        .clear { background-color: #ff9800; color: white; }
        button:hover { opacity: 0.8; }
        .log { height: 300px; overflow-y: auto; border: 1px solid #ddd; padding: 10px; background-color: #fafafa; font-family: monospace; font-size: 12px; }
        .log-entry { margin: 2px 0; padding: 2px 0; }
        .timestamp { color: #666; }
        .performance { background-color: #e3f2fd; padding: 10px; margin: 10px 0; border-radius: 4px; font-size: 12px; }
    </style>
</head>
<body>
    <div class="container">
        <h1>League of Legends 自動承認アプリ (Go版)</h1>
        <div class="performance">
            <strong>完全自動:</strong> アプリ起動と同時に「対戦を検出中」画面を監視開始 → 自動で承認ボタンクリック → 5秒後に画面が変わったら監視停止
        </div>
        <div id="status" class="status stopped">ステータス: 停止中</div>
        <div class="buttons">
            <button class="start" onclick="sendAction('start')">監視開始</button>
            <button class="stop" onclick="sendAction('stop')">監視停止</button>
            <button class="test" onclick="sendAction('test')">パフォーマンステスト</button>
            <button class="clear" onclick="clearLog()">ログクリア</button>
        </div>
        <h3>ログ:</h3>
        <div id="log" class="log">
            <div class="log-entry">LoL Auto Accept へようこそ (完全自動版)<br>
            アプリ起動と同時に「対戦を検出中」画面の監視を開始しました</div>
        </div>
    </div>
    
    <script>
        const ws = new WebSocket('ws://localhost:8081/ws');
        
        ws.onmessage = function(event) {
            const data = JSON.parse(event.data);
            if (data.type === 'log') {
                addLog(data);
            } else if (data.type === 'status') {
                updateStatus(data);
            }
        };
        
        function addLog(data) {
            const log = document.getElementById('log');
            const entry = document.createElement('div');
            entry.className = 'log-entry';
            entry.innerHTML = '<span class="timestamp">[' + data.timestamp + ']</span> ' + data.message;
            log.appendChild(entry);
            log.scrollTop = log.scrollHeight;
        }
        
        function updateStatus(data) {
            const status = document.getElementById('status');
            status.textContent = 'ステータス: ' + data.status;
            status.className = 'status ' + (data.status === '監視中...' ? 'running' : 'stopped');
        }
        
        function sendAction(action) {
            ws.send(JSON.stringify({action: action}));
        }
        
        function clearLog() {
            document.getElementById('log').innerHTML = '<div class="log-entry">ログをクリアしました</div>';
        }
    </script>
</body>
</html>
`
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(htmlContent))
}

func (s *Server) SetupRoutes() *mux.Router {
	r := mux.NewRouter()
	r.HandleFunc("/", s.ServeHTML)
	r.HandleFunc("/ws", s.HandleWebSocket)
	
	staticDir, _ := filepath.Abs("./resources")
	r.PathPrefix("/resources/").Handler(http.StripPrefix("/resources/", http.FileServer(http.Dir(staticDir))))
	
	return r
}

func (s *Server) OpenBrowser(url string) {
	var err error
	switch runtime.GOOS {
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = fmt.Errorf("unsupported platform")
	}
	if err != nil {
		// エラーは無視（ブラウザが開けなくても動作に支障なし）
	}
}

func (s *Server) Start() error {
	r := s.SetupRoutes()
	
	// 自動監視を開始
	go s.app.StartAutoWatcher()
	
	go func() {
		time.Sleep(1 * time.Second)
		s.OpenBrowser("http://localhost:8081")
	}()
	
	return http.ListenAndServe(":8081", r)
}