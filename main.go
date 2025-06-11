package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"os/exec"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/kbinani/screenshot"
)

type App struct {
	running       bool
	mutex         sync.RWMutex
	logWidget     *widget.Entry
	statusText    *widget.Label
	acceptTemplate image.Image
	matchingTemplate image.Image
}

func NewApp() *App {
	return &App{
		running: false,
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

func (a *App) log(message string) {
	timestamp := time.Now().Format("15:04:05")
	logMessage := fmt.Sprintf("[%s] %s\n", timestamp, message)
	a.logWidget.SetText(a.logWidget.Text + logMessage)
}

func (a *App) updateStatus(status string) {
	a.statusText.SetText(fmt.Sprintf("ステータス: %s", status))
}

func (a *App) startMonitoring() {
	if a.isRunning() {
		return
	}

	// テンプレート画像の読み込み
	if err := a.loadTemplates(); err != nil {
		a.log(fmt.Sprintf("テンプレート読み込みエラー: %v", err))
		return
	}

	a.setRunning(true)
	a.updateStatus("監視中...")
	a.log("監視を開始しました")

	go func() {
		for a.isRunning() {
			// まずマッチング画面があるかチェック
			if a.detectMatchingScreen() {
				a.log("マッチング画面を検出")
				
				// 承認ボタンを探す
				buttonPos := a.detectAcceptButton()
				if buttonPos != nil {
					a.log(fmt.Sprintf("承認ボタンを検出しました (位置: %d, %d)", buttonPos.X, buttonPos.Y))
					if a.clickAcceptButton(buttonPos.X, buttonPos.Y) {
						a.log("承認ボタンをクリックしました")
						// クリック後少し待機
						time.Sleep(3 * time.Second)
					} else {
						a.log("承認ボタンのクリックに失敗しました")
					}
				}
			}
			time.Sleep(1000 * time.Millisecond) // 1秒間隔で監視
		}
	}()
}

func (a *App) stopMonitoring() {
	if !a.isRunning() {
		return
	}

	a.setRunning(false)
	a.updateStatus("停止中")
	a.log("監視を停止しました")
}

type Point struct {
	X, Y int
}

func (a *App) detectMatchingScreen() bool {
	// スクリーンショットを取得
	bounds := screenshot.GetDisplayBounds(0)
	img, err := screenshot.CaptureRect(bounds)
	if err != nil {
		return false
	}

	// テンプレートマッチングでマッチング画面を検出
	matchPos := a.templateMatch(img, a.matchingTemplate, 0.8)
	return matchPos != nil
}

func (a *App) detectAcceptButton() *Point {
	// スクリーンショットを取得
	bounds := screenshot.GetDisplayBounds(0)
	img, err := screenshot.CaptureRect(bounds)
	if err != nil {
		a.log(fmt.Sprintf("スクリーンショットの取得に失敗: %v", err))
		return nil
	}

	// テンプレートマッチングで承認ボタンを検出
	return a.templateMatch(img, a.acceptTemplate, 0.7)
}

func (a *App) templateMatch(haystack *image.RGBA, needle image.Image, threshold float64) *Point {
	haystackBounds := haystack.Bounds()
	needleBounds := needle.Bounds()
	
	needleWidth := needleBounds.Dx()
	needleHeight := needleBounds.Dy()
	
	var bestMatch *Point
	bestScore := threshold
	
	// テンプレートマッチング実行
	for y := 0; y <= haystackBounds.Dy()-needleHeight; y += 2 { // 2ピクセルずつスキップして高速化
		for x := 0; x <= haystackBounds.Dx()-needleWidth; x += 2 {
			score := a.calculateSimilarity(haystack, needle, x, y)
			if score > bestScore {
				bestScore = score
				bestMatch = &Point{X: x + needleWidth/2, Y: y + needleHeight/2}
			}
		}
	}
	
	return bestMatch
}

func (a *App) calculateSimilarity(haystack *image.RGBA, needle image.Image, offsetX, offsetY int) float64 {
	needleBounds := needle.Bounds()
	totalPixels := 0
	matchingPixels := 0
	
	for y := 0; y < needleBounds.Dy(); y++ {
		for x := 0; x < needleBounds.Dx(); x++ {
			haystackX := offsetX + x
			haystackY := offsetY + y
			
			if haystackX < haystack.Bounds().Max.X && haystackY < haystack.Bounds().Max.Y {
				haystackColor := haystack.RGBAAt(haystackX, haystackY)
				needleColor := needle.At(needleBounds.Min.X+x, needleBounds.Min.Y+y)
				
				// RGBAカラーに変換
				nr, ng, nb, na := needleColor.RGBA()
				nr, ng, nb, na = nr>>8, ng>>8, nb>>8, na>>8
				
				// 色の類似度を計算
				if a.colorsAreSimilar(haystackColor, nr, ng, nb, na) {
					matchingPixels++
				}
				totalPixels++
			}
		}
	}
	
	if totalPixels == 0 {
		return 0
	}
	
	return float64(matchingPixels) / float64(totalPixels)
}

func (a *App) colorsAreSimilar(c1 color.RGBA, r2, g2, b2, a2 uint32) bool {
	// 色の差異を計算（簡単なユークリッド距離）
	dr := float64(c1.R) - float64(r2)
	dg := float64(c1.G) - float64(g2)
	db := float64(c1.B) - float64(b2)
	
	distance := math.Sqrt(dr*dr + dg*dg + db*db)
	return distance < 50 // 閾値は調整可能
}

func (a *App) clickAcceptButton(x, y int) bool {
	// xdotoolを使用してクリック（Linuxの場合）
	cmd := exec.Command("xdotool", "mousemove", fmt.Sprintf("%d", x), fmt.Sprintf("%d", y))
	err := cmd.Run()
	if err != nil {
		a.log(fmt.Sprintf("マウス移動に失敗: %v", err))
		return false
	}

	// 少し待ってからクリック
	time.Sleep(100 * time.Millisecond)

	cmd = exec.Command("xdotool", "click", "1")
	err = cmd.Run()
	if err != nil {
		a.log(fmt.Sprintf("マウスクリックに失敗: %v", err))
		return false
	}

	return true
}

func (a *App) createUI() {
	myApp := app.New()
	window := myApp.NewWindow("LoL Auto Accept - Go版")
	window.Resize(fyne.Size{Width: 600, Height: 500})

	// ステータス表示
	a.statusText = widget.NewLabel("ステータス: 停止中")

	// ボタン
	startButton := widget.NewButton("監視開始", func() {
		a.startMonitoring()
	})

	stopButton := widget.NewButton("監視停止", func() {
		a.stopMonitoring()
	})

	clearLogButton := widget.NewButton("ログクリア", func() {
		a.logWidget.SetText("LoL Auto Accept へようこそ (テンプレートマッチング版)\n")
	})

	testButton := widget.NewButton("テスト", func() {
		bounds := screenshot.GetDisplayBounds(0)
		a.log(fmt.Sprintf("画面サイズ: %dx%d", bounds.Dx(), bounds.Dy()))
		
		// テンプレート読み込みテスト
		if err := a.loadTemplates(); err != nil {
			a.log(fmt.Sprintf("テンプレート読み込みエラー: %v", err))
		} else {
			a.log("テンプレート読み込み成功")
		}
		
		// xdotoolの存在確認
		cmd := exec.Command("which", "xdotool")
		err := cmd.Run()
		if err != nil {
			a.log("xdotoolがインストールされていません。sudo apt-get install xdotool を実行してください。")
		} else {
			a.log("xdotoolが利用可能です。")
		}
	})

	// ログ表示
	a.logWidget = widget.NewMultiLineEntry()
	a.logWidget.SetText("LoL Auto Accept へようこそ (テンプレートマッチング版)\n" +
		"使用前にxdotoolをインストールしてください：sudo apt-get install xdotool\n" +
		"resourcesフォルダにテンプレート画像を配置してください\n")
	a.logWidget.Disable()

	// レイアウト
	buttonContainer := container.NewHBox(startButton, stopButton, clearLogButton, testButton)
	content := container.NewVBox(
		widget.NewLabel("League of Legends 自動承認アプリ (Go版 - テンプレートマッチング)"),
		widget.NewSeparator(),
		a.statusText,
		buttonContainer,
		widget.NewLabel("ログ:"),
		container.NewScroll(a.logWidget),
	)

	window.SetContent(content)
	window.ShowAndRun()
}

func main() {
	app := NewApp()
	
	// 初期化ログ
	log.Println("LoL Auto Accept アプリを起動中...")
	
	app.createUI()
}