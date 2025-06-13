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
	autoWatching     bool
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
		autoWatching:    false,
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

func (a *App) isAutoWatching() bool {
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	return a.autoWatching
}

func (a *App) setAutoWatching(watching bool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.autoWatching = watching
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
	a.setWaitingForMatch(true) // 最初はマッチング画面を待機
	a.updateStatus("マッチング画面待機中...")
	a.sendLog("自動監視を開始しました - マッチング画面を検出中")

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
						// 5秒に1回マッチング待機状況をログ出力
						if time.Now().Unix()%5 == 0 {
							bounds := img.Bounds()
							a.sendLog(fmt.Sprintf("マッチング画面を待機中... (画面サイズ: %dx%d)", bounds.Dx(), bounds.Dy()))
						}
					}
				} else {
					// 承認ボタンを監視中
					// まずマッチング画面がまだ存在するかチェック
					if !a.fastDetectMatchingScreen(img) {
						// マッチング画面が検出されなくなった場合、監視を停止
						a.sendLog("マッチング画面が検出されなくなりました - 監視を自動停止します")
						a.stopMonitoring()
						return
					}
					
					buttonPos := a.fastDetectAcceptButton(img)
					if buttonPos != nil {
						elapsed := time.Since(start)
						
						// 詳細検証スコアを取得
						verifyScore := a.verifyAcceptButton(img, buttonPos, 1.0)
						a.sendLog(fmt.Sprintf("承認ボタンを検出しました (位置: %d, %d, 検証スコア: %.3f, 検出時間: %v)", 
							buttonPos.X, buttonPos.Y, verifyScore, elapsed))
						
						// より低い閾値でも許可（検証スコアが低くてもクリック）
						if verifyScore > 0.2 {
							if a.clickAcceptButton(buttonPos.X, buttonPos.Y) {
								a.sendLog("承認ボタンをクリックしました")
								a.sendLog("5秒待機後、マッチング画面の状態をチェックします")
								// 5秒待機
								time.Sleep(5 * time.Second)
								// 5秒後にマッチング画面が検出されるかチェック
								img2, err := a.captureScreen()
								if err == nil && !a.fastDetectMatchingScreen(img2) {
									a.sendLog("マッチング画面が検出されなくなりました - 監視を自動停止します")
									a.stopMonitoring()
									return
								} else {
									a.sendLog("マッチング画面が継続中 - 監視を継続します")
								}
							} else {
								a.sendLog("承認ボタンのクリックに失敗しました")
							}
						} else {
							a.sendLog(fmt.Sprintf("検証スコアが低いため、クリックをスキップしました (スコア: %.3f)", verifyScore))
						}
					} else {
						// 10秒に1回承認ボタン検索状況をログ出力（頻度を上げる）
						if time.Now().Unix()%10 == 0 {
							elapsed := time.Since(start)
							a.sendLog(fmt.Sprintf("承認ボタンを検索中... (検索時間: %v)", elapsed))
							// デバッグ: 検索エリアの情報も出力
							bounds := img.Bounds()
							a.sendLog(fmt.Sprintf("検索エリア: 画面サイズ %dx%d, 中央下部を重点検索", bounds.Dx(), bounds.Dy()))
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

// 自動監視機能：matching.pngを検出したら自動で監視開始
func (a *App) startAutoWatcher() {
	// テンプレート画像の読み込み
	if err := a.loadTemplates(); err != nil {
		log.Printf("テンプレート読み込みエラー: %v", err)
		return
	}

	a.setAutoWatching(true)
	log.Println("自動監視を開始しました - matching.png を検出中...")

	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for a.isAutoWatching() {
			select {
			case <-ticker.C:
				// 既に監視中の場合はスキップ
				if a.isRunning() {
					continue
				}

				// スクリーンショット取得
				img, err := a.captureScreen()
				if err != nil {
					continue
				}

				// マッチング画面を検出
				if a.fastDetectMatchingScreen(img) {
					a.sendLog("マッチング画面を検出 - 自動監視を開始します")
					a.startMonitoring()
				}
			}
		}
	}()
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
		searchArea := image.Rect(0, 0, bounds.Dx(), bounds.Dy()) // 全体を検索
		if pos := a.templateMatchFast(img, a.matchingTemplate, 0.6, searchArea, 1.0); pos != nil {
			return true
		}
		
		// 複数スケールでも試行
		scales := []float64{0.5, 0.7, 0.8, 1.2, 1.5, 2.0}
		for _, scale := range scales {
			if pos := a.templateMatchFast(img, a.matchingTemplate, 0.5, searchArea, scale); pos != nil {
				return true
			}
		}
	}
	
	// 2. 文字検出による方法（「対戦を検出中」の文字を探す）
	// 「対戦を検出中」の特徴的な色パターンを検出
	matchingTextCount := 0
	totalSamples := 0
	
	// 画面中央付近を重点的に検索
	centerX := bounds.Dx() / 2
	centerY := bounds.Dy() / 2
	searchRect := image.Rect(
		centerX-200, centerY-100,
		centerX+200, centerY+100,
	)
	
	// 境界チェック
	if searchRect.Min.X < 0 {
		searchRect.Min.X = 0
	}
	if searchRect.Min.Y < 0 {
		searchRect.Min.Y = 0
	}
	if searchRect.Max.X > bounds.Dx() {
		searchRect.Max.X = bounds.Dx()
	}
	if searchRect.Max.Y > bounds.Dy() {
		searchRect.Max.Y = bounds.Dy()
	}
	
	// 3ピクセルごとにサンプリング
	for y := searchRect.Min.Y; y < searchRect.Max.Y; y += 3 {
		for x := searchRect.Min.X; x < searchRect.Max.X; x += 3 {
			c := img.RGBAAt(x, y)
			totalSamples++
			
			// 「対戦を検出中」の文字の色（白っぽい色）を検出
			if c.R > 200 && c.G > 200 && c.B > 200 {
				matchingTextCount++
			}
		}
	}
	
	// 白い文字の割合をチェック
	if totalSamples > 0 {
		textRatio := float64(matchingTextCount) / float64(totalSamples)
		if textRatio > 0.05 { // 5%以上の白い文字があれば検出
			return true
		}
	}
	
	return false
}

// 高精度承認ボタン検出（複数手法併用）
func (a *App) fastDetectAcceptButton(img *image.RGBA) *Point {
	bounds := img.Bounds()
	centerX := bounds.Dx() / 2
	centerY := bounds.Dy() / 2
	
	// より広い検索範囲（画面下部全体）
	searchArea := image.Rect(
		centerX-400, centerY-50,
		centerX+400, centerY+250,
	)
	
	// 境界チェック
	if searchArea.Min.X < 0 {
		searchArea.Min.X = 0
	}
	if searchArea.Min.Y < 0 {
		searchArea.Min.Y = 0
	}
	if searchArea.Max.X > bounds.Dx() {
		searchArea.Max.X = bounds.Dx()
	}
	if searchArea.Max.Y > bounds.Dy() {
		searchArea.Max.Y = bounds.Dy()
	}
	
	// 手法1: テンプレートマッチング（複数スケール・低閾値）
	scales := []float64{0.5, 0.6, 0.7, 0.8, 0.9, 1.0, 1.1, 1.2, 1.3, 1.5}
	thresholds := []float64{0.4, 0.5, 0.6, 0.7}
	
	var bestMatch *Point
	var bestScore float64
	
	for _, threshold := range thresholds {
		for _, scale := range scales {
			if pos := a.templateMatchFast(img, a.acceptTemplate, threshold, searchArea, scale); pos != nil {
				score := a.verifyAcceptButton(img, pos, scale)
				if score > bestScore {
					bestScore = score
					bestMatch = pos
				}
			}
		}
	}
	
	// 手法2: 色ベース検出（青緑のボタン色を検出）
	if bestMatch == nil {
		bestMatch = a.detectButtonByColor(img, searchArea)
	}
	
	// 手法3: エッジ検出（ボタンの輪郭を検出）
	if bestMatch == nil {
		bestMatch = a.detectButtonByEdge(img, searchArea)
	}
	
	// デバッグ情報
	if bestMatch != nil {
		a.sendLog(fmt.Sprintf("承認ボタン候補を検出: 位置(%d, %d), スコア: %.3f", bestMatch.X, bestMatch.Y, bestScore))
	}
	
	return bestMatch
}

// 色ベース検出（青緑のボタン色を検出）
func (a *App) detectButtonByColor(img *image.RGBA, searchArea image.Rectangle) *Point {
	var bestCandidate *Point
	maxClusterSize := 0
	
	// 青緑色のピクセルをクラスタリング
	for y := searchArea.Min.Y; y < searchArea.Max.Y; y += 2 {
		for x := searchArea.Min.X; x < searchArea.Max.X; x += 2 {
			c := img.RGBAAt(x, y)
			
			// 承認ボタンの特徴的な青緑色を検出
			if a.isAcceptButtonColor(c) {
				// 周囲の類似色ピクセルをカウント
				clusterSize := a.countSimilarColorCluster(img, x, y, searchArea)
				if clusterSize > maxClusterSize && clusterSize > 50 {
					maxClusterSize = clusterSize
					bestCandidate = &Point{X: x, Y: y}
				}
			}
		}
	}
	
	return bestCandidate
}

// 承認ボタンの色判定
func (a *App) isAcceptButtonColor(c color.RGBA) bool {
	// 承認ボタンの青緑色（複数パターン）
	return (c.G > c.R+20 && c.G > c.B+10 && c.G > 100) || // 緑が強い
		   (c.B > c.R+20 && c.G > c.R+10 && c.B > 80) ||   // 青が強い
		   (c.G > 120 && c.B > 80 && c.R < 100)              // 青緑色
}

// 類似色クラスタのサイズをカウント
func (a *App) countSimilarColorCluster(img *image.RGBA, centerX, centerY int, bounds image.Rectangle) int {
	count := 0
	radius := 20
	
	for dy := -radius; dy <= radius; dy++ {
		for dx := -radius; dx <= radius; dx++ {
			x := centerX + dx
			y := centerY + dy
			
			if x >= bounds.Min.X && x < bounds.Max.X && y >= bounds.Min.Y && y < bounds.Max.Y {
				c := img.RGBAAt(x, y)
				if a.isAcceptButtonColor(c) {
					count++
				}
			}
		}
	}
	
	return count
}

// エッジ検出による承認ボタン検出
func (a *App) detectButtonByEdge(img *image.RGBA, searchArea image.Rectangle) *Point {
	// ボタンの矩形エッジを検出
	for y := searchArea.Min.Y; y < searchArea.Max.Y-50; y += 5 {
		for x := searchArea.Min.X; x < searchArea.Max.X-100; x += 5 {
			// 50x25のエリアでボタンらしい形状を検索
			if a.isButtonShape(img, x, y, 100, 50) {
				return &Point{X: x + 50, Y: y + 25} // 中心点を返す
			}
		}
	}
	
	return nil
}

// ボタンの形状判定
func (a *App) isButtonShape(img *image.RGBA, x, y, width, height int) bool {
	bounds := img.Bounds()
	if x+width >= bounds.Max.X || y+height >= bounds.Max.Y {
		return false
	}
	
	edgeCount := 0
	totalPixels := 0
	
	// 周囲のエッジを検出
	for dy := 0; dy < height; dy += 2 {
		for dx := 0; dx < width; dx += 2 {
			px := x + dx
			py := y + dy
			
			if px < bounds.Max.X-1 && py < bounds.Max.Y-1 {
				c1 := img.RGBAAt(px, py)
				c2 := img.RGBAAt(px+1, py)
				c3 := img.RGBAAt(px, py+1)
				
				// エッジ検出（色の変化が大きい場所）
				if a.colorDifference(c1, c2) > 30 || a.colorDifference(c1, c3) > 30 {
					edgeCount++
				}
				totalPixels++
			}
		}
	}
	
	// エッジの密度でボタン判定
	return totalPixels > 0 && float64(edgeCount)/float64(totalPixels) > 0.15
}

// 色の差分計算
func (a *App) colorDifference(c1, c2 color.RGBA) int {
	dr := int(c1.R) - int(c2.R)
	dg := int(c1.G) - int(c2.G)
	db := int(c1.B) - int(c2.B)
	
	if dr < 0 {
		dr = -dr
	}
	if dg < 0 {
		dg = -dg
	}
	if db < 0 {
		db = -db
	}
	
	return dr + dg + db
}

// 承認ボタンの詳細検証（より緩い条件）
func (a *App) verifyAcceptButton(img *image.RGBA, pos *Point, scale float64) float64 {
	if a.acceptTemplate == nil {
		return 0.5 // テンプレートがない場合でも基本スコアを返す
	}
	
	needleBounds := a.acceptTemplate.Bounds()
	needleWidth := int(float64(needleBounds.Dx()) * scale)
	needleHeight := int(float64(needleBounds.Dy()) * scale)
	
	// ボタン位置の中心から実際のテンプレート領域を計算
	startX := pos.X - needleWidth/2
	startY := pos.Y - needleHeight/2
	
	// 境界チェック
	if startX < 0 || startY < 0 || startX+needleWidth >= img.Bounds().Max.X || startY+needleHeight >= img.Bounds().Max.Y {
		return 0.3 // 境界外でも低いスコアで継続
	}
	
	// より詳細な類似度計算
	score := a.calculateDetailedSimilarity(img, a.acceptTemplate, startX, startY, scale)
	
	// 周囲の色も考慮してスコアを調整
	colorBonus := a.checkSurroundingColors(img, pos.X, pos.Y)
	return score + colorBonus
}

// 周囲の色をチェックしてスコアを調整
func (a *App) checkSurroundingColors(img *image.RGBA, centerX, centerY int) float64 {
	bounds := img.Bounds()
	blueGreenCount := 0
	totalChecked := 0
	
	// 周囲20ピクセルの範囲をチェック
	for dy := -20; dy <= 20; dy += 4 {
		for dx := -20; dx <= 20; dx += 4 {
			x := centerX + dx
			y := centerY + dy
			
			if x >= 0 && x < bounds.Dx() && y >= 0 && y < bounds.Dy() {
				c := img.RGBAAt(x, y)
				if a.isAcceptButtonColor(c) {
					blueGreenCount++
				}
				totalChecked++
			}
		}
	}
	
	if totalChecked == 0 {
		return 0
	}
	
	// 青緑色の割合に応じてボーナススコア
	ratio := float64(blueGreenCount) / float64(totalChecked)
	if ratio > 0.3 {
		return 0.2 // 30%以上なら大きなボーナス
	} else if ratio > 0.1 {
		return 0.1 // 10%以上なら小さなボーナス
	}
	
	return 0
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
	
	// より細かい検索間隔
	step := 2
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
	
	// より細かいサンプリング
	sampleStep := 2
	
	for y := 0; y < needleBounds.Dy(); y += sampleStep {
		for x := 0; x < needleBounds.Dx(); x += sampleStep {
			haystackX := offsetX + int(float64(x)*scale)
			haystackY := offsetY + int(float64(y)*scale)
			
			if haystackX >= haystack.Bounds().Max.X || haystackY >= haystack.Bounds().Max.Y {
				continue
			}
			
			haystackColor := haystack.RGBAAt(haystackX, haystackY)
			needleColor := needle.At(needleBounds.Min.X+x, needleBounds.Min.Y+y)
			
			nr, ng, nb, na := needleColor.RGBA()
			nr, ng, nb, na = nr>>8, ng>>8, nb>>8, na>>8
			
			// より緩い色類似度判定
			if a.colorsAreSimilarLoose(haystackColor, nr, ng, nb) {
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

// より緩い色類似度判定
func (a *App) colorsAreSimilarLoose(c1 color.RGBA, r2, g2, b2 uint32) bool {
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
	
	return (dr + dg + db) < 150 // より緩い閾値
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
		// テンプレートサイズ情報
		if a.acceptTemplate != nil {
			acceptBounds := a.acceptTemplate.Bounds()
			a.sendLog(fmt.Sprintf("承認ボタンテンプレートサイズ: %dx%d", acceptBounds.Dx(), acceptBounds.Dy()))
		}
		if a.matchingTemplate != nil {
			matchingBounds := a.matchingTemplate.Bounds()
			a.sendLog(fmt.Sprintf("マッチングテンプレートサイズ: %dx%d", matchingBounds.Dx(), matchingBounds.Dy()))
		}
	}
	
	// 検出速度テスト
	img, err := a.captureScreen()
	if err == nil {
		testStart := time.Now()
		matchingDetected := a.fastDetectMatchingScreen(img)
		elapsed := time.Since(testStart)
		a.sendLog(fmt.Sprintf("マッチング画面検出テスト: %v (結果: %v)", elapsed, matchingDetected))
		
		// 複数手法での承認ボタン検出テスト
		testStart = time.Now()
		buttonPos := a.fastDetectAcceptButton(img)
		elapsed = time.Since(testStart)
		buttonDetected := buttonPos != nil
		
		if buttonDetected {
			verifyScore := a.verifyAcceptButton(img, buttonPos, 1.0)
			a.sendLog(fmt.Sprintf("承認ボタン検出テスト: %v (結果: %v, 検証スコア: %.3f, 位置: %d,%d)", 
				elapsed, buttonDetected, verifyScore, buttonPos.X, buttonPos.Y))
		} else {
			a.sendLog(fmt.Sprintf("承認ボタン検出テスト: %v (結果: %v)", elapsed, buttonDetected))
			
			// 検出できない場合の詳細分析
			centerX := img.Bounds().Dx() / 2
			centerY := img.Bounds().Dy() / 2
			searchArea := image.Rect(centerX-400, centerY-50, centerX+400, centerY+250)
			
			// 色ベース検出テスト
			colorPos := a.detectButtonByColor(img, searchArea)
			if colorPos != nil {
				a.sendLog(fmt.Sprintf("色ベース検出で候補発見: 位置(%d, %d)", colorPos.X, colorPos.Y))
			}
			
			// エッジ検出テスト
			edgePos := a.detectButtonByEdge(img, searchArea)
			if edgePos != nil {
				a.sendLog(fmt.Sprintf("エッジ検出で候補発見: 位置(%d, %d)", edgePos.X, edgePos.Y))
			}
		}
		
		// 承認ボタン色の分析
		buttonColorCount := 0
		totalSamples := 0
		centerX := img.Bounds().Dx() / 2
		centerY := img.Bounds().Dy() / 2
		
		for y := centerY; y < centerY+200 && y < img.Bounds().Dy(); y += 5 {
			for x := centerX-200; x < centerX+200 && x < img.Bounds().Dx(); x += 5 {
				if x >= 0 && y >= 0 {
					c := img.RGBAAt(x, y)
					totalSamples++
					if a.isAcceptButtonColor(c) {
						buttonColorCount++
					}
				}
			}
		}
		
		if totalSamples > 0 {
			buttonColorRatio := float64(buttonColorCount) / float64(totalSamples)
			a.sendLog(fmt.Sprintf("承認ボタン色分析: %.2f%% (サンプル数: %d)", buttonColorRatio*100, totalSamples))
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

	// 接続時に現在の状態を送信
	if a.isRunning() {
		a.updateStatus("監視中...")
	} else if a.isAutoWatching() {
		a.updateStatus("自動監視中...")
	} else {
		a.updateStatus("停止中")
	}

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
	log.Println("サーバー起動: http://localhost:8081")
	log.Println("最適化済み: 高速検出アルゴリズム搭載")
	
	// 自動監視を開始
	go app.startAutoWatcher()
	
	go func() {
		time.Sleep(1 * time.Second)
		app.openBrowser("http://localhost:8081")
	}()
	
	log.Fatal(http.ListenAndServe(":8081", r))
}