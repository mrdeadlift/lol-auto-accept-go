package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/kbinani/screenshot"
)

type App struct {
	running          bool
	waitingForMatch  bool
	mutex            sync.RWMutex
	acceptTemplate   image.Image
	matchingTemplate image.Image
	clients          map[*websocket.Conn]bool
	clientsMutex     sync.RWMutex
	lastScreenshot   *image.RGBA
	screenBounds     image.Rectangle
}

type LogMessage struct {
	Type      string `json:"type"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

type StatusUpdate struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func NewApp() *App {
	return &App{
		running:         false,
		waitingForMatch: false,
		clients:         make(map[*websocket.Conn]bool),
	}
}

func (a *App) loadTemplates() error {
	// 承認ボタンのテンプレート読み込み
	acceptFile, err := os.Open("resources/accept_button.png")
	if err != nil {
		return fmt.Errorf("承認ボタンテンプレートの読み込み失敗: %v", err)
	}
	defer acceptFile.Close()

	acceptImg, err := png.Decode(acceptFile)
	if err != nil {
		return fmt.Errorf("承認ボタン画像のデコード失敗: %v", err)
	}
	a.acceptTemplate = acceptImg

	// マッチング画面のテンプレート読み込み
	matchingFile, err := os.Open("resources/matching.png")
	if err != nil {
		return fmt.Errorf("マッチングテンプレートの読み込み失敗: %v", err)
	}
	defer matchingFile.Close()

	matchingImg, err := png.Decode(matchingFile)
	if err != nil {
		return fmt.Errorf("マッチング画像のデコード失敗: %v", err)
	}
	a.matchingTemplate = matchingImg

	return nil
}

func (a *App) isRunning() bool {
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	return a.running
}

func (a *App) setRunning(running bool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.running = running
}

func (a *App) isWaitingForMatch() bool {
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	return a.waitingForMatch
}

func (a *App) setWaitingForMatch(waiting bool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.waitingForMatch = waiting
}

func (a *App) broadcastMessage(message interface{}) {
	a.clientsMutex.RLock()
	defer a.clientsMutex.RUnlock()

	jsonData, _ := json.Marshal(message)
	for client := range a.clients {
		err := client.WriteMessage(websocket.TextMessage, jsonData)
		if err != nil {
			client.Close()
			delete(a.clients, client)
		}
	}
}

func (a *App) sendLog(message string) {
	logMsg := LogMessage{
		Type:      "log",
		Message:   message,
		Timestamp: time.Now().Format("15:04:05"),
	}
	a.broadcastMessage(logMsg)
}

func (a *App) updateStatus(status string) {
	statusMsg := StatusUpdate{
		Type:   "status",
		Status: status,
	}
	a.broadcastMessage(statusMsg)
}

func (a *App) captureScreen() (*image.RGBA, error) {
	bounds := screenshot.GetDisplayBounds(0)
	img, err := screenshot.CaptureRect(bounds)
	if err != nil {
		return nil, err
	}
	a.lastScreenshot = img
	a.screenBounds = bounds
	return img, nil
}

func (a *App) startMonitoring() {
	if a.isRunning() {
		return
	}

	// テンプレート画像の読み込み
	if err := a.loadTemplates(); err != nil {
		a.sendLog(fmt.Sprintf("テンプレート読み込みエラー: %v", err))
		return
	}

	a.setRunning(true)
	a.setWaitingForMatch(false)
	a.updateStatus("監視中...")
	a.sendLog("監視を開始しました（自動制御モード）")

	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for a.isRunning() {
			select {
			case <-ticker.C:
				start := time.Now()
				
				// スクリーンショット取得
				img, err := a.captureScreen()
				if err != nil {
					continue
				}

				if a.isWaitingForMatch() {
					// マッチング画面を待機中
					if a.fastDetectMatchingScreen(img) {
						a.sendLog("マッチング画面を検出 - 承認ボタン監視を開始")
						a.setWaitingForMatch(false)
						a.updateStatus("承認ボタン監視中...")
					} else {
						// 5秒に1回マッチング待機状況をログ出力（頻度を上げる）
						if time.Now().Unix()%5 == 0 {
							// デバッグ情報を含める
							bounds := img.Bounds()
							a.sendLog(fmt.Sprintf("マッチング画面を待機中... (画面サイズ: %dx%d)", bounds.Dx(), bounds.Dy()))
						}
					}
				} else {
					// 承認ボタンを監視中
					buttonPos := a.fastDetectAcceptButton(img)
					if buttonPos != nil {
						elapsed := time.Since(start)
						
						// 詳細検証スコアを取得
						verifyScore := a.verifyAcceptButton(img, buttonPos, 1.0)
						a.sendLog(fmt.Sprintf("承認ボタンを検出しました (位置: %d, %d, 検証スコア: %.2f, 検出時間: %v)", 
							buttonPos.X, buttonPos.Y, verifyScore, elapsed))
						
						if a.clickAcceptButton(buttonPos.X, buttonPos.Y) {
							a.sendLog("承認ボタンをクリックしました")
							a.sendLog("マッチング完了 - 次のマッチング画面を待機します")
							a.setWaitingForMatch(true)
							a.updateStatus("マッチング画面待機中...")
							// クリック後の待機時間を短縮
							time.Sleep(1 * time.Second)
						} else {
							a.sendLog("承認ボタンのクリックに失敗しました")
						}
					} else {
						// 20秒に1回承認ボタン検索状況をログ出力
						if time.Now().Unix()%20 == 0 {
							elapsed := time.Since(start)
							a.sendLog(fmt.Sprintf("承認ボタンを検索中... (検索時間: %v)", elapsed))
						}
					}
				}
			}
		}
	}()
}

func (a *App) stopMonitoring() {
	if !a.isRunning() {
		return
	}

	a.setRunning(false)
	a.setWaitingForMatch(false)
	a.updateStatus("停止中")
	a.sendLog("監視を停止しました")
}

type Point struct {
	X, Y int
}

// 高速マッチング画面検出（テンプレートマッチング + 特徴点ベース）
func (a *App) fastDetectMatchingScreen(img *image.RGBA) bool {
	bounds := img.Bounds()
	
	// 1. テンプレートマッチングによる検出
	if a.matchingTemplate != nil {
		// 画面全体でマッチングテンプレートを検索（低い閾値で）
		searchArea := image.Rect(0, 0, bounds.Dx(), bounds.Dy()/2) // 上半分のみ
		if pos := a.templateMatchFast(img, a.matchingTemplate, 0.3, searchArea, 1.0); pos != nil {
			return true
		}
		
		// 複数スケールでも試行
		scales := []float64{0.7, 0.8, 1.2, 1.5}
		for _, scale := range scales {
			if pos := a.templateMatchFast(img, a.matchingTemplate, 0.25, searchArea, scale); pos != nil {
				return true
			}
		}
	}
	
	// 2. 特徴的な色検出（バックアップ方法）
	searchHeight := bounds.Dy() / 2 // 上半分を検索
	blueCount := 0
	darkCount := 0
	samplePoints := 0
	
	// 5ピクセルごとにサンプリング
	for y := 0; y < searchHeight; y += 5 {
		for x := 0; x < bounds.Dx(); x += 5 {
			c := img.RGBAAt(x, y)
			samplePoints++
			
			// マッチング画面の特徴的な色を検出
			// 1. 青系の色（マッチング画面のUI）
			if c.B > 80 && c.B > c.R+20 && c.B > c.G+15 {
				blueCount++
			}
			
			// 2. 暗い色（マッチング画面の背景）
			if c.R < 50 && c.G < 50 && c.B < 80 {
				darkCount++
			}
		}
	}
	
	// 特徴的な色の割合をチェック
	if samplePoints > 0 {
		blueRatio := float64(blueCount) / float64(samplePoints)
		darkRatio := float64(darkCount) / float64(samplePoints)
		
		// より緩い条件で検出
		return blueRatio > 0.01 || darkRatio > 0.3 // 青1%以上 または 暗色30%以上
	}
	
	return false
}

// 高速承認ボタン検出（領域限定 + マルチスケール + 厳格検証）
func (a *App) fastDetectAcceptButton(img *image.RGBA) *Point {
	bounds := img.Bounds()
	centerX := bounds.Dx() / 2
	centerY := bounds.Dy() / 2
	
	// 検索範囲を画面中央下部に限定（承認ボタンの一般的な位置）
	searchArea := image.Rect(
		centerX-250, centerY+50,  // より狭い範囲に限定
		centerX+250, centerY+200,
	)
	
	// 複数スケールで検索
	scales := []float64{0.8, 1.0, 1.2} // 80%, 100%, 120%
	
	var bestMatch *Point
	var bestScore float64
	
	for _, scale := range scales {
		if pos := a.templateMatchFast(img, a.acceptTemplate, 0.75, searchArea, scale); pos != nil {
			// より高い閾値で二重チェック
			score := a.verifyAcceptButton(img, pos, scale)
			if score > bestScore && score > 0.8 {
				bestScore = score
				bestMatch = pos
			}
		}
	}
	
	return bestMatch
}

// 承認ボタンの詳細検証
func (a *App) verifyAcceptButton(img *image.RGBA, pos *Point, scale float64) float64 {
	if a.acceptTemplate == nil {
		return 0
	}
	
	needleBounds := a.acceptTemplate.Bounds()
	needleWidth := int(float64(needleBounds.Dx()) * scale)
	needleHeight := int(float64(needleBounds.Dy()) * scale)
	
	// ボタン位置の中心から実際のテンプレート領域を計算
	startX := pos.X - needleWidth/2
	startY := pos.Y - needleHeight/2
	
	// 境界チェック
	if startX < 0 || startY < 0 || startX+needleWidth >= img.Bounds().Max.X || startY+needleHeight >= img.Bounds().Max.Y {
		return 0
	}
	
	// より詳細な類似度計算
	return a.calculateDetailedSimilarity(img, a.acceptTemplate, startX, startY, scale)
}

// 詳細類似度計算（ピクセル単位での厳密チェック）
func (a *App) calculateDetailedSimilarity(haystack *image.RGBA, needle image.Image, offsetX, offsetY int, scale float64) float64 {
	needleBounds := needle.Bounds()
	totalPixels := 0
	matchingPixels := 0
	
	// 全ピクセルをチェック（サンプリングなし）
	for y := 0; y < needleBounds.Dy(); y++ {
		for x := 0; x < needleBounds.Dx(); x++ {
			haystackX := offsetX + int(float64(x)*scale)
			haystackY := offsetY + int(float64(y)*scale)
			
			if haystackX >= haystack.Bounds().Max.X || haystackY >= haystack.Bounds().Max.Y {
				continue
			}
			
			haystackColor := haystack.RGBAAt(haystackX, haystackY)
			needleColor := needle.At(needleBounds.Min.X+x, needleBounds.Min.Y+y)
			
			nr, ng, nb, na := needleColor.RGBA()
			nr, ng, nb, na = nr>>8, ng>>8, nb>>8, na>>8
			
			// より厳密な色判定
			if a.colorsAreSimilarStrict(haystackColor, nr, ng, nb) {
				matchingPixels++
			}
			totalPixels++
		}
	}
	
	if totalPixels == 0 {
		return 0
	}
	
	return float64(matchingPixels) / float64(totalPixels)
}

// 厳密な色類似度判定
func (a *App) colorsAreSimilarStrict(c1 color.RGBA, r2, g2, b2 uint32) bool {
	// より厳しい閾値
	dr := int(c1.R) - int(r2)
	dg := int(c1.G) - int(g2)
	db := int(c1.B) - int(b2)
	
	if dr < 0 {
		dr = -dr
	}
	if dg < 0 {
		dg = -dg
	}
	if db < 0 {
		db = -db
	}
	
	return (dr + dg + db) < 60 // より厳しい閾値
}

// 高速テンプレートマッチング（最適化版）
func (a *App) templateMatchFast(haystack *image.RGBA, needle image.Image, threshold float64, searchArea image.Rectangle, scale float64) *Point {
	needleBounds := needle.Bounds()
	needleWidth := int(float64(needleBounds.Dx()) * scale)
	needleHeight := int(float64(needleBounds.Dy()) * scale)
	
	// 検索範囲を制限
	haystackBounds := haystack.Bounds()
	if searchArea.Min.X < haystackBounds.Min.X {
		searchArea.Min.X = haystackBounds.Min.X
	}
	if searchArea.Min.Y < haystackBounds.Min.Y {
		searchArea.Min.Y = haystackBounds.Min.Y
	}
	if searchArea.Max.X > haystackBounds.Max.X {
		searchArea.Max.X = haystackBounds.Max.X
	}
	if searchArea.Max.Y > haystackBounds.Max.Y {
		searchArea.Max.Y = haystackBounds.Max.Y
	}
	
	var bestMatch *Point
	bestScore := threshold
	
	// 5ピクセルごとにスキップして大幅に高速化
	step := 5
	for y := searchArea.Min.Y; y <= searchArea.Max.Y-needleHeight; y += step {
		for x := searchArea.Min.X; x <= searchArea.Max.X-needleWidth; x += step {
			// 高速類似度計算（サンプリングベース）
			score := a.calculateSimilarityFast(haystack, needle, x, y, scale)
			if score > bestScore {
				bestScore = score
				bestMatch = &Point{X: x + needleWidth/2, Y: y + needleHeight/2}
			}
		}
	}
	
	return bestMatch
}

// 高速類似度計算（サンプリングベース）
func (a *App) calculateSimilarityFast(haystack *image.RGBA, needle image.Image, offsetX, offsetY int, scale float64) float64 {
	needleBounds := needle.Bounds()
	totalSamples := 0
	matchingSamples := 0
	
	// サンプリング間隔を調整（高速化）
	sampleStep := 3
	scaleInv := 1.0 / scale
	
	for y := 0; y < needleBounds.Dy(); y += sampleStep {
		for x := 0; x < needleBounds.Dx(); x += sampleStep {
			haystackX := offsetX + int(float64(x)*scale)
			haystackY := offsetY + int(float64(y)*scale)
			
			if haystackX >= haystack.Bounds().Max.X || haystackY >= haystack.Bounds().Max.Y {
				continue
			}
			
			haystackColor := haystack.RGBAAt(haystackX, haystackY)
			
			// スケールに応じてニードル座標を調整
			needleX := needleBounds.Min.X + int(float64(x)*scaleInv)
			needleY := needleBounds.Min.Y + int(float64(y)*scaleInv)
			
			if needleX >= needleBounds.Max.X || needleY >= needleBounds.Max.Y {
				continue
			}
			
			needleColor := needle.At(needleX, needleY)
			nr, ng, nb, na := needleColor.RGBA()
			nr, ng, nb, na = nr>>8, ng>>8, nb>>8, na>>8
			
			// 高速色類似度判定
			if a.colorsAreSimilarFast(haystackColor, nr, ng, nb) {
				matchingSamples++
			}
			totalSamples++
		}
	}
	
	if totalSamples == 0 {
		return 0
	}
	
	return float64(matchingSamples) / float64(totalSamples)
}

// 高速色類似度判定（簡略化）
func (a *App) colorsAreSimilarFast(c1 color.RGBA, r2, g2, b2 uint32) bool {
	// Manhattan距離を使用（平方根計算を避けて高速化）
	dr := int(c1.R) - int(r2)
	dg := int(c1.G) - int(g2)
	db := int(c1.B) - int(b2)
	
	if dr < 0 {
		dr = -dr
	}
	if dg < 0 {
		dg = -dg
	}
	if db < 0 {
		db = -db
	}
	
	return (dr + dg + db) < 120 // 閾値調整
}

func (a *App) clickAcceptButton(x, y int) bool {
	var err error
	
	if runtime.GOOS == "windows" {
		err = a.clickWindows(x, y)
	} else {
		err = a.clickUnix(x, y)
	}
	
	return err == nil
}

func (a *App) clickWindows(x, y int) error {
	script := fmt.Sprintf(`
Add-Type -AssemblyName System.Windows.Forms
[System.Windows.Forms.Cursor]::Position = [System.Drawing.Point]::new(%d, %d)
Start-Sleep -Milliseconds 50
Add-Type -TypeDefinition '
using System;
using System.Runtime.InteropServices;
public class Mouse {
    [DllImport("user32.dll")]
    public static extern void mouse_event(uint dwFlags, uint dx, uint dy, uint dwData, IntPtr dwExtraInfo);
    public const uint MOUSEEVENTF_LEFTDOWN = 0x02;
    public const uint MOUSEEVENTF_LEFTUP = 0x04;
}
'
[Mouse]::mouse_event([Mouse]::MOUSEEVENTF_LEFTDOWN, 0, 0, 0, [IntPtr]::Zero)
Start-Sleep -Milliseconds 50
[Mouse]::mouse_event([Mouse]::MOUSEEVENTF_LEFTUP, 0, 0, 0, [IntPtr]::Zero)
`, x, y)

	cmd := exec.Command("powershell", "-Command", script)
	return cmd.Run()
}

func (a *App) clickUnix(x, y int) error {
	cmd := exec.Command("xdotool", "mousemove", fmt.Sprintf("%d", x), fmt.Sprintf("%d", y))
	err := cmd.Run()
	if err != nil {
		return err
	}

	time.Sleep(50 * time.Millisecond)

	cmd = exec.Command("xdotool", "click", "1")
	return cmd.Run()
}

func (a *App) testEnvironment() {
	start := time.Now()
	
	bounds := screenshot.GetDisplayBounds(0)
	a.sendLog(fmt.Sprintf("画面サイズ: %dx%d", bounds.Dx(), bounds.Dy()))
	a.sendLog(fmt.Sprintf("OS: %s", runtime.GOOS))
	
	if err := a.loadTemplates(); err != nil {
		a.sendLog(fmt.Sprintf("テンプレート読み込みエラー: %v", err))
	} else {
		a.sendLog("テンプレート読み込み成功")
	}
	
	// 検出速度テスト
	img, err := a.captureScreen()
	if err == nil {
		testStart := time.Now()
		matchingDetected := a.fastDetectMatchingScreen(img)
		elapsed := time.Since(testStart)
		a.sendLog(fmt.Sprintf("マッチング画面検出テスト: %v (結果: %v)", elapsed, matchingDetected))
		
		testStart = time.Now()
		buttonPos := a.fastDetectAcceptButton(img)
		elapsed = time.Since(testStart)
		buttonDetected := buttonPos != nil
		
		if buttonDetected {
			verifyScore := a.verifyAcceptButton(img, buttonPos, 1.0)
			a.sendLog(fmt.Sprintf("承認ボタン検出テスト: %v (結果: %v, 検証スコア: %.2f, 位置: %d,%d)", 
				elapsed, buttonDetected, verifyScore, buttonPos.X, buttonPos.Y))
		} else {
			a.sendLog(fmt.Sprintf("承認ボタン検出テスト: %v (結果: %v)", elapsed, buttonDetected))
		}
		
		// 色分析テスト
		bounds := img.Bounds()
		blueCount := 0
		darkCount := 0
		totalSamples := 0
		searchHeight := bounds.Dy() / 2
		
		for y := 0; y < searchHeight; y += 10 {
			for x := 0; x < bounds.Dx(); x += 10 {
				c := img.RGBAAt(x, y)
				totalSamples++
				
				if c.B > 80 && c.B > c.R+20 && c.B > c.G+15 {
					blueCount++
				}
				if c.R < 50 && c.G < 50 && c.B < 80 {
					darkCount++
				}
			}
		}
		
		if totalSamples > 0 {
			blueRatio := float64(blueCount) / float64(totalSamples)
			darkRatio := float64(darkCount) / float64(totalSamples)
			a.sendLog(fmt.Sprintf("色分析: 青色%.1f%%, 暗色%.1f%%", blueRatio*100, darkRatio*100))
		}
	}
	
	if runtime.GOOS == "windows" {
		a.sendLog("Windows環境: PowerShellでマウス制御")
	} else {
		cmd := exec.Command("which", "xdotool")
		err := cmd.Run()
		if err != nil {
			a.sendLog("xdotoolがインストールされていません")
		} else {
			a.sendLog("xdotoolが利用可能です")
		}
	}
	
	totalElapsed := time.Since(start)
	a.sendLog(fmt.Sprintf("環境テスト完了: %v", totalElapsed))
}

func (a *App) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}
	defer conn.Close()

	a.clientsMutex.Lock()
	a.clients[conn] = true
	a.clientsMutex.Unlock()

	defer func() {
		a.clientsMutex.Lock()
		delete(a.clients, conn)
		a.clientsMutex.Unlock()
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
			a.startMonitoring()
		case "stop":
			a.stopMonitoring()
		case "test":
			a.testEnvironment()
		}
	}
}

func (a *App) serveHTML(w http.ResponseWriter, r *http.Request) {
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
            <strong>自動制御:</strong> マッチング画面検出 → 承認ボタンクリック → 次のマッチング待機の自動サイクル
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
            <div class="log-entry">LoL Auto Accept へようこそ (自動制御版)<br>
            マッチング検出から承認まで完全自動化されました</div>
        </div>
    </div>
    
    <script>
        const ws = new WebSocket('ws://localhost:8080/ws');
        
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

func (a *App) openBrowser(url string) {
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
		log.Printf("ブラウザの起動に失敗: %v", err)
	}
}

func main() {
	app := NewApp()
	
	r := mux.NewRouter()
	r.HandleFunc("/", app.serveHTML)
	r.HandleFunc("/ws", app.handleWebSocket)
	
	staticDir, _ := filepath.Abs("./resources")
	r.PathPrefix("/resources/").Handler(http.StripPrefix("/resources/", http.FileServer(http.Dir(staticDir))))
	
	log.Println("LoL Auto Accept アプリを起動中...")
	log.Println("サーバー起動: http://localhost:8080")
	log.Println("最適化済み: 高速検出アルゴリズム搭載")
	
	go func() {
		time.Sleep(1 * time.Second)
		app.openBrowser("http://localhost:8080")
	}()
	
	log.Fatal(http.ListenAndServe(":8080", r))
}