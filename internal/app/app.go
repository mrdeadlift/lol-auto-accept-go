package app

import (
	"fmt"
	"sync"
	"time"

	"lol-auto-accept/internal/detector"
	"lol-auto-accept/internal/system"
	"lol-auto-accept/internal/websocket"
)

type App struct {
	running         bool
	waitingForMatch bool
	autoWatching    bool
	mutex           sync.RWMutex
	
	detector     *detector.ImageDetector
	wsManager    *websocket.Manager
	systemCtrl   *system.Controller
}

func NewApp() *App {
	return &App{
		running:         false,
		waitingForMatch: false,
		autoWatching:    false,
		detector:        detector.NewImageDetector(),
		wsManager:       websocket.NewManager(),
		systemCtrl:      system.NewController(),
	}
}

func (a *App) GetWebSocketManager() *websocket.Manager {
	return a.wsManager
}

func (a *App) IsRunning() bool {
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	return a.running
}

func (a *App) SetRunning(running bool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.running = running
}

func (a *App) IsWaitingForMatch() bool {
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	return a.waitingForMatch
}

func (a *App) SetWaitingForMatch(waiting bool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.waitingForMatch = waiting
}

func (a *App) IsAutoWatching() bool {
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	return a.autoWatching
}

func (a *App) SetAutoWatching(watching bool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.autoWatching = watching
}

func (a *App) StartMonitoring() {
	if a.IsRunning() {
		return
	}

	// テンプレート画像の読み込み
	if err := a.detector.LoadTemplates(); err != nil {
		a.wsManager.SendLog(fmt.Sprintf("テンプレート読み込みエラー: %v", err))
		return
	}

	a.SetRunning(true)
	a.SetWaitingForMatch(true) // 最初はマッチング画面を待機
	a.wsManager.UpdateStatus("マッチング画面待機中...")
	a.wsManager.SendLog("自動監視を開始しました - マッチング画面を検出中")

	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for a.IsRunning() {
			<-ticker.C
			start := time.Now()
			
			// スクリーンショット取得
			img, err := a.detector.CaptureScreen()
			if err != nil {
				continue
			}

			if a.IsWaitingForMatch() {
				// マッチング画面を待機中
				if a.detector.FastDetectMatchingScreen(img) {
					a.wsManager.SendLog("マッチング画面を検出 - 承認ボタン監視を開始")
					a.SetWaitingForMatch(false)
					a.wsManager.UpdateStatus("承認ボタン監視中...")
				} else {
					// 5秒に1回マッチング待機状況をログ出力
					if time.Now().Unix()%5 == 0 {
						bounds := img.Bounds()
						a.wsManager.SendLog(fmt.Sprintf("マッチング画面を待機中... (画面サイズ: %dx%d)", bounds.Dx(), bounds.Dy()))
					}
				}
			} else {
				// 承認ボタンを監視中
				// まずマッチング画面がまだ存在するかチェック
				if !a.detector.FastDetectMatchingScreen(img) {
					// マッチング画面が検出されなくなった場合、監視を停止
					a.wsManager.SendLog("マッチング画面が検出されなくなりました - 監視を自動停止します")
					a.StopMonitoring()
					return
				}
				
				buttonPos := a.detector.FastDetectAcceptButton(img)
				if buttonPos != nil {
					elapsed := time.Since(start)
					
					// 詳細検証スコアを取得
					verifyScore := a.detector.VerifyAcceptButton(img, buttonPos, 1.0)
					a.wsManager.SendLog(fmt.Sprintf("承認ボタンを検出しました (位置: %d, %d, 検証スコア: %.3f, 検出時間: %v)", 
						buttonPos.X, buttonPos.Y, verifyScore, elapsed))
					
					// より低い閾値でも許可（検証スコアが低くてもクリック）
					if verifyScore > 0.2 {
						if a.systemCtrl.ClickAcceptButton(buttonPos.X, buttonPos.Y) {
							a.wsManager.SendLog("承認ボタンをクリックしました")
							a.wsManager.SendLog("5秒待機後、マッチング画面の状態をチェックします")
							// 5秒待機
							time.Sleep(5 * time.Second)
							// 5秒後にマッチング画面が検出されるかチェック
							img2, err := a.detector.CaptureScreen()
							if err == nil && !a.detector.FastDetectMatchingScreen(img2) {
								a.wsManager.SendLog("マッチング画面が検出されなくなりました - 監視を自動停止します")
								a.StopMonitoring()
								return
							} else {
								a.wsManager.SendLog("マッチング画面が継続中 - 監視を継続します")
							}
						} else {
							a.wsManager.SendLog("承認ボタンのクリックに失敗しました")
						}
					} else {
						a.wsManager.SendLog(fmt.Sprintf("検証スコアが低いため、クリックをスキップしました (スコア: %.3f)", verifyScore))
					}
				} else {
					// 10秒に1回承認ボタン検索状況をログ出力（頻度を上げる）
					if time.Now().Unix()%10 == 0 {
						elapsed := time.Since(start)
						a.wsManager.SendLog(fmt.Sprintf("承認ボタンを検索中... (検索時間: %v)", elapsed))
						// デバッグ: 検索エリアの情報も出力
						bounds := img.Bounds()
						a.wsManager.SendLog(fmt.Sprintf("検索エリア: 画面サイズ %dx%d, 中央下部を重点検索", bounds.Dx(), bounds.Dy()))
					}
				}
			}
		}
	}()
}

func (a *App) StopMonitoring() {
	if !a.IsRunning() {
		return
	}

	a.SetRunning(false)
	a.SetWaitingForMatch(false)
	a.wsManager.UpdateStatus("停止中")
	a.wsManager.SendLog("監視を停止しました")
}

// 自動監視機能：matching.pngを検出したら自動で監視開始
func (a *App) StartAutoWatcher() {
	// テンプレート画像の読み込み
	if err := a.detector.LoadTemplates(); err != nil {
		return
	}

	a.SetAutoWatching(true)

	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for a.IsAutoWatching() {
			<-ticker.C
			// 既に監視中の場合はスキップ
			if a.IsRunning() {
				continue
			}

			// スクリーンショット取得
			img, err := a.detector.CaptureScreen()
			if err != nil {
				continue
			}

			// マッチング画面を検出
			if a.detector.FastDetectMatchingScreen(img) {
				a.wsManager.SendLog("マッチング画面を検出 - 自動監視を開始します")
				a.StartMonitoring()
			}
		}
	}()
}

func (a *App) TestEnvironment() {
	start := time.Now()
	
	// 画面サイズを取得するため最初にスクリーンショットを撮る
	img, err := a.detector.CaptureScreen()
	if err == nil {
		bounds := img.Bounds()
		a.wsManager.SendLog(fmt.Sprintf("画面サイズ: %dx%d", bounds.Dx(), bounds.Dy()))
	}
	a.wsManager.SendLog(fmt.Sprintf("OS: %s", a.systemCtrl.GetOSName()))
	
	if err := a.detector.LoadTemplates(); err != nil {
		a.wsManager.SendLog(fmt.Sprintf("テンプレート読み込みエラー: %v", err))
	} else {
		a.wsManager.SendLog("テンプレート読み込み成功")
		// テンプレートサイズ情報
		if acceptTemplate := a.detector.GetAcceptTemplate(); acceptTemplate != nil {
			acceptBounds := acceptTemplate.Bounds()
			a.wsManager.SendLog(fmt.Sprintf("承認ボタンテンプレートサイズ: %dx%d", acceptBounds.Dx(), acceptBounds.Dy()))
		}
		if matchingTemplate := a.detector.GetMatchingTemplate(); matchingTemplate != nil {
			matchingBounds := matchingTemplate.Bounds()
			a.wsManager.SendLog(fmt.Sprintf("マッチングテンプレートサイズ: %dx%d", matchingBounds.Dx(), matchingBounds.Dy()))
		}
	}
	
	// 検出速度テスト
	if err == nil {
		testStart := time.Now()
		matchingDetected := a.detector.FastDetectMatchingScreen(img)
		elapsed := time.Since(testStart)
		a.wsManager.SendLog(fmt.Sprintf("マッチング画面検出テスト: %v (結果: %v)", elapsed, matchingDetected))
		
		// 複数手法での承認ボタン検出テスト
		testStart = time.Now()
		buttonPos := a.detector.FastDetectAcceptButton(img)
		elapsed = time.Since(testStart)
		buttonDetected := buttonPos != nil
		
		if buttonDetected {
			verifyScore := a.detector.VerifyAcceptButton(img, buttonPos, 1.0)
			a.wsManager.SendLog(fmt.Sprintf("承認ボタン検出テスト: %v (結果: %v, 検証スコア: %.3f, 位置: %d,%d)", 
				elapsed, buttonDetected, verifyScore, buttonPos.X, buttonPos.Y))
		} else {
			a.wsManager.SendLog(fmt.Sprintf("承認ボタン検出テスト: %v (結果: %v)", elapsed, buttonDetected))
		}
	}
	
	if !a.systemCtrl.IsSystemSupported() {
		a.wsManager.SendLog("システム制御が利用できません")
	} else {
		a.wsManager.SendLog("システム制御が利用可能です")
	}
	
	totalElapsed := time.Since(start)
	a.wsManager.SendLog(fmt.Sprintf("環境テスト完了: %v", totalElapsed))
}