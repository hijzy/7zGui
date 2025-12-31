package main

import (
	"bytes"
	"errors"
	"fmt"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// =========================
// 全局配置区(禁止 CLI 传参)
// =========================

const (
	WINDOW_TITLE              = "7zz 解压助手"
	WINDOW_WIDTH      float32 = 920
	WINDOW_HEIGHT     float32 = 600
	SEVEN_ZZ_BASENAME         = "7zz"

	// 列表列宽配置
	COL_WIDTH_SIZE   float32 = 120 // 大小列宽度, 单位为像素
	COL_WIDTH_PACKED float32 = 120 // 解压后列宽度, 单位为像素
	COL_WIDTH_TIME   float32 = 160 // 修改时间列宽度, 单位为像素
	COL_WIDTH_TYPE   float32 = 80  // 类型列宽度, 单位为像素

	// 表头背景颜色配置 (RGBA Hex)
	HEADER_BG_COLOR = "#F5F5F5" // 浅灰色背景

	// 字体配置
	FONT_FILE_NAME = "NotoSansSC-Regular.ttf" // 字体文件名

	// 拖拽提示区域配置
	DROP_HINT_PADDING      float32 = 16        // 拖拽提示框距离窗口边缘的间距
	DROP_HINT_BORDER_COLOR         = "#888888" // 拖拽提示框边框颜色
	DROP_HINT_TEXT_COLOR           = "#888888" // 拖拽提示文字颜色

	// 对话框尺寸配置
	DIALOG_MIN_WIDTH  float32 = 300 // 统一对话框最小宽度
	DIALOG_MIN_HEIGHT float32 = 100 // 统一对话框最小高度
)

var (
	currentFile     string
	currentPassword string
	sevenZipPath    string
	dropCounter     atomic.Uint64
)

// myTheme 实现了 fyne.Theme 接口，用于强制指定字体
type myTheme struct{}

func (m *myTheme) Font(s fyne.TextStyle) fyne.Resource {
	// 无论什么样式，都返回同一个字体文件
	// 这里使用 LoadResourceFromPath 加载本地文件
	// 注意: 生产环境可能需要打包字体，这里为了满足用户"本地脚本"和"指定字体"的需求
	path := getResourcePath(FONT_FILE_NAME)
	r, err := fyne.LoadResourceFromPath(path)
	if err != nil {
		// 如果加载失败，回退到默认字体，避免崩溃
		return theme.DefaultTheme().Font(s)
	}
	return r
}

func (m *myTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	return theme.DefaultTheme().Color(n, v)
}

func (m *myTheme) Icon(n fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(n)
}

func (m *myTheme) Size(n fyne.ThemeSizeName) float32 {
	return theme.DefaultTheme().Size(n)
}

func init() {
	sevenZipPath = resolve7zzPath()
	if sevenZipPath != SEVEN_ZZ_BASENAME {
		_ = os.Chmod(sevenZipPath, 0o755)
	}
}

type archiveItem struct {
	name     string
	size     uint64
	packed   uint64
	modified string
	attr     string
	isDir    bool
}

func main() {
	myApp := app.New()
	// 应用自定义主题
	myApp.Settings().SetTheme(&myTheme{})

	// 设置应用图标
	// 尝试加载本地或资源目录中的 Icon.png
	iconPath := getResourcePath("Icon.png")
	if iconRes, err := fyne.LoadResourceFromPath(iconPath); err == nil {
		myApp.SetIcon(iconRes)
	}

	myWindow := myApp.NewWindow(WINDOW_TITLE)
	myWindow.Resize(fyne.NewSize(WINDOW_WIDTH, WINDOW_HEIGHT))

	columns := []string{"名称", "大小", "解压后", "修改时间", "类型"}
	items := make([]archiveItem, 0, 256)

	dropHint := newDropHint()

	// 使用 List 替代 Table
	list := widget.NewList(
		func() int { return len(items) },
		func() fyne.CanvasObject {
			// 创建列表项布局
			icon := widget.NewIcon(nil)
			nameLbl := widget.NewLabel("")
			nameLbl.Truncation = fyne.TextTruncateEllipsis
			nameLbl.TextStyle = fyne.TextStyle{Bold: true}

			sizeLbl := widget.NewLabel("")
			sizeLbl.Alignment = fyne.TextAlignLeading

			packedLbl := widget.NewLabel("")
			packedLbl.Alignment = fyne.TextAlignLeading

			timeLbl := widget.NewLabel("")
			timeLbl.Alignment = fyne.TextAlignLeading

			attrLbl := widget.NewLabel("")
			attrLbl.Alignment = fyne.TextAlignLeading

			// 自定义布局容器
			return container.New(newFileListLayout(),
				icon, nameLbl, sizeLbl, packedLbl, timeLbl, attrLbl)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id < 0 || id >= len(items) {
				return
			}
			c := obj.(*fyne.Container)
			icon := c.Objects[0].(*widget.Icon)
			nameLbl := c.Objects[1].(*widget.Label)
			sizeLbl := c.Objects[2].(*widget.Label)
			packedLbl := c.Objects[3].(*widget.Label)
			timeLbl := c.Objects[4].(*widget.Label)
			attrLbl := c.Objects[5].(*widget.Label)

			entry := items[id]

			// 设置图标
			if entry.isDir {
				icon.SetResource(theme.FolderIcon())
			} else {
				lowerName := strings.ToLower(entry.name)
				if strings.HasSuffix(lowerName, ".png") ||
					strings.HasSuffix(lowerName, ".jpg") ||
					strings.HasSuffix(lowerName, ".jpeg") ||
					strings.HasSuffix(lowerName, ".gif") ||
					strings.HasSuffix(lowerName, ".bmp") ||
					strings.HasSuffix(lowerName, ".webp") {
					icon.SetResource(theme.FileImageIcon())
				} else {
					icon.SetResource(theme.FileIcon())
				}
			}

			// 设置文本
			if entry.isDir {
				nameLbl.SetText(entry.name + "/")
				sizeLbl.SetText("")
				packedLbl.SetText("")
				attrLbl.SetText("文件夹")
			} else {
				nameLbl.SetText(entry.name)
				sizeLbl.SetText(formatSize(entry.packed))
				packedLbl.SetText(formatSize(entry.size))
				attrLbl.SetText("文件")
			}
			timeLbl.SetText(entry.modified)
		},
	)

	var extractBtn *widget.Button
	extractBtn = widget.NewButton("解压到当前目录", func() {
		if currentFile == "" {
			return
		}
		token := dropCounter.Load()
		startExtract(myWindow, token, currentFile, currentPassword, extractBtn)
	})
	extractBtn.Importance = widget.LowImportance
	extractBtn.Disable()
	extractBtnBg := canvas.NewRectangle(parseHexColor(HEADER_BG_COLOR))
	extractBar := container.NewStack(extractBtnBg, extractBtn)

	// 创建自定义表头
	header := createListHeader(columns)
	listPage := container.NewBorder(header, extractBar, nil, nil, list)
	listPage.Hide()

	contentStack := container.NewStack(dropHint, listPage)
	content := container.NewBorder(nil, nil, nil, nil, contentStack)

	// 设置背景色，稍微区别于列表
	bg := canvas.NewRectangle(theme.BackgroundColor())
	mainContainer := container.NewStack(bg, content)

	myWindow.SetContent(mainContainer)

	myWindow.SetOnDropped(func(pos fyne.Position, uris []fyne.URI) {
		if len(uris) == 0 {
			return
		}

		filePath := uris[0].Path()
		if filePath == "" {
			return
		}
		filePath = filepath.Clean(filePath)

		info, err := os.Stat(filePath)
		if err != nil {
			dialog.ShowError(fmt.Errorf("无法读取文件: %s", err.Error()), myWindow)
			return
		}
		if info.IsDir() {
			dialog.ShowInformation("提示", "请拖入单个压缩文件, 不要拖入文件夹", myWindow)
			return
		}

		token := dropCounter.Add(1)
		currentFile = filePath
		currentPassword = ""

		items = items[:0]
		list.Refresh()
		extractBtn.Disable()

		dropHint.Hide()
		listPage.Show()
		startListFiles(myWindow, token, filePath, "", &items, list, extractBtn)
	})

	myWindow.ShowAndRun()
}

func startListFiles(win fyne.Window, token uint64, archivePath string, password string, items *[]archiveItem, list *widget.List, btn *widget.Button) {
	go func() {
		output, err := run7zzList(archivePath, password)

		fyne.Do(func() {
			if token != dropCounter.Load() || archivePath != currentFile {
				return
			}

			if err != nil && is7zzNotFound(err) {
				dialog.ShowError(fmt.Errorf("找不到 7zz.\n请把 7zz 文件和本程序放在同一个文件夹.\n当前尝试路径: %s", sevenZipPath), win)
				return
			}

			if needsPassword(output) {
				showPasswordDialog(win, token, archivePath, items, list, btn)
				return
			}

			if err != nil {
				dialog.ShowError(fmt.Errorf("%s", output), win)
				return
			}

			parsed := parse7zzListSlt(output)
			*items = append((*items)[:0], parsed...)
			list.Refresh()
			btn.Enable()
		})
	}()
}

// 包装内容以确保最小尺寸
func wrapWithMinSize(content fyne.CanvasObject) fyne.CanvasObject {
	// 使用透明矩形撑开尺寸
	spacer := canvas.NewRectangle(color.Transparent)
	spacer.SetMinSize(fyne.NewSize(DIALOG_MIN_WIDTH, DIALOG_MIN_HEIGHT))

	// 使用 Stack 布局，使 spacer 撑开容器，content 覆盖在上面
	// 注意：这里不强制居中，由 content 自身决定布局 (如 Label 会自动填充，其他可能需要 Center)
	return container.NewStack(spacer, content)
}

func showPasswordDialog(win fyne.Window, token uint64, archivePath string, items *[]archiveItem, list *widget.List, btn *widget.Button) {
	pwdEntry := widget.NewPasswordEntry()
	pwdEntry.PlaceHolder = "请输入密码"

	// 限制输入框宽度
	entryWrapper := container.NewGridWrap(fyne.NewSize(300, 40), pwdEntry)

	// 提示信息
	fileName := filepath.Base(archivePath)
	msg := fmt.Sprintf("请输入压缩包密码:\n%s", fileName)
	msgLabel := widget.NewLabel(msg)
	msgLabel.Alignment = fyne.TextAlignCenter

	// 组合内容：垂直排列 文本 + 输入框
	// 使用 Center 布局让输入框居中显示
	vbox := container.NewVBox(msgLabel, container.NewCenter(entryWrapper))

	// 居中整个内容块在对话框中
	centeredContent := container.NewCenter(vbox)

	// 强制最小尺寸
	content := wrapWithMinSize(centeredContent)

	d := dialog.NewCustomConfirm("需要密码", "确定", "取消", content, func(ok bool) {
		if !ok {
			return
		}

		currentPassword := pwdEntry.Text
		btn.Disable()
		*items = (*items)[:0]
		list.Refresh()
		startListFiles(win, token, archivePath, currentPassword, items, list, btn)
	}, win)

	// 显示对话框
	d.Show()

	// 尝试聚焦输入框 (Fyne 的聚焦有时候需要延时或在 Show 之后)
	win.Canvas().Focus(pwdEntry)
}

// ---------------------------------------------------------
// 自定义布局相关代码
// ---------------------------------------------------------

type fileListLayout struct{}

func newFileListLayout() fyne.Layout {
	return &fileListLayout{}
}

func (l *fileListLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	// objects 顺序: icon, name, size, packed, time, attr
	if len(objects) < 6 {
		return
	}

	// 从右向左布局固定宽度的列
	x := size.Width
	h := size.Height

	centerY := func(obj fyne.CanvasObject) (float32, float32) {
		mh := obj.MinSize().Height
		if mh <= 0 {
			return 0, h
		}
		if mh > h {
			mh = h
		}
		return (h - mh) / 2, mh
	}

	maxTextH := float32(0)
	for i := 1; i <= 5; i++ {
		mh := objects[i].MinSize().Height
		if mh > maxTextH {
			maxTextH = mh
		}
	}
	if maxTextH <= 0 {
		maxTextH = h
	}
	if maxTextH > h {
		maxTextH = h
	}
	textY := (h - maxTextH) / 2

	// Attr
	x -= COL_WIDTH_TYPE
	objects[5].Resize(fyne.NewSize(COL_WIDTH_TYPE, maxTextH))
	objects[5].Move(fyne.NewPos(x, textY))

	// Time
	x -= COL_WIDTH_TIME
	objects[4].Resize(fyne.NewSize(COL_WIDTH_TIME, maxTextH))
	objects[4].Move(fyne.NewPos(x, textY))

	// Packed
	x -= COL_WIDTH_PACKED
	objects[3].Resize(fyne.NewSize(COL_WIDTH_PACKED, maxTextH))
	objects[3].Move(fyne.NewPos(x, textY))

	// Size
	x -= COL_WIDTH_SIZE
	objects[2].Resize(fyne.NewSize(COL_WIDTH_SIZE, maxTextH))
	objects[2].Move(fyne.NewPos(x, textY))

	// Icon
	iconW := float32(theme.IconInlineSize())
	y, hh := centerY(objects[0])
	objects[0].Resize(fyne.NewSize(iconW, hh))
	objects[0].Move(fyne.NewPos(0, y))

	// Name (剩余空间)
	nameX := iconW + theme.Padding()
	nameW := x - nameX - theme.Padding()
	if nameW < 0 {
		nameW = 0
	}
	objects[1].Resize(fyne.NewSize(nameW, maxTextH))
	objects[1].Move(fyne.NewPos(nameX, textY))
}

func (l *fileListLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	h := theme.IconInlineSize() + 12 // 增加高度，避免文字重叠
	return fyne.NewSize(COL_WIDTH_SIZE+COL_WIDTH_PACKED+COL_WIDTH_TIME+COL_WIDTH_TYPE+100, h)
}

func createListHeader(columns []string) fyne.CanvasObject {
	// 创建表头标签
	nameLbl := widget.NewLabel(columns[0])
	nameLbl.TextStyle = fyne.TextStyle{Bold: true}

	sizeLbl := widget.NewLabel(columns[1])
	sizeLbl.TextStyle = fyne.TextStyle{Bold: true}
	sizeLbl.Alignment = fyne.TextAlignLeading

	packedLbl := widget.NewLabel(columns[2])
	packedLbl.TextStyle = fyne.TextStyle{Bold: true}
	packedLbl.Alignment = fyne.TextAlignLeading

	timeLbl := widget.NewLabel(columns[3])
	timeLbl.TextStyle = fyne.TextStyle{Bold: true}
	timeLbl.Alignment = fyne.TextAlignLeading

	attrLbl := widget.NewLabel(columns[4])
	attrLbl.TextStyle = fyne.TextStyle{Bold: true}
	attrLbl.Alignment = fyne.TextAlignLeading

	// 使用相同的布局，但第一个元素放一个空的占位符代替图标
	spacer := canvas.NewRectangle(color.Transparent)

	// 使用自定义布局容器
	c := container.New(newFileListLayout(),
		spacer, nameLbl, sizeLbl, packedLbl, timeLbl, attrLbl)

	// 添加背景和分割线
	// 使用自定义颜色作为表头背景，确保与列表内容区分明显
	bg := canvas.NewRectangle(parseHexColor(HEADER_BG_COLOR))
	line := canvas.NewRectangle(theme.ShadowColor())
	line.SetMinSize(fyne.NewSize(0, 1))

	return container.NewBorder(nil, line, nil, nil,
		container.NewStack(bg, c))
}

func startExtract(win fyne.Window, token uint64, archivePath string, password string, btn *widget.Button) {
	btn.Disable()
	outputDir := defaultOutputDir(archivePath)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		btn.Enable()
		dialog.ShowError(fmt.Errorf("无法创建目录: %s", err.Error()), win)
		return
	}

	go func() {
		output, err := run7zzExtract(archivePath, outputDir, password)

		fyne.Do(func() {
			if token != dropCounter.Load() || archivePath != currentFile {
				return
			}

			if err != nil && is7zzNotFound(err) {
				dialog.ShowError(fmt.Errorf("找不到 7zz.\n请把 7zz 文件和本程序放在同一个文件夹.\n当前尝试路径: %s", sevenZipPath), win)
				return
			}

			if needsPassword(output) {
				pwdEntry := widget.NewPasswordEntry()
				pwdEntry.PlaceHolder = "请输入密码"

				// 限制输入框宽度
				entryWrapper := container.NewGridWrap(fyne.NewSize(300, 40), pwdEntry)

				// 提示信息
				fileName := filepath.Base(archivePath)
				msg := fmt.Sprintf("请输入压缩包密码:\n%s", fileName)
				msgLabel := widget.NewLabel(msg)
				msgLabel.Alignment = fyne.TextAlignCenter

				vbox := container.NewVBox(msgLabel, container.NewCenter(entryWrapper))
				centeredContent := container.NewCenter(vbox)
				content := wrapWithMinSize(centeredContent)

				d := dialog.NewCustomConfirm("需要密码", "确定", "取消", content, func(ok bool) {
					if !ok {
						btn.Enable()
						return
					}
					currentPassword = pwdEntry.Text
					startExtract(win, token, archivePath, currentPassword, btn)
				}, win)
				d.Show()
				win.Canvas().Focus(pwdEntry)
				return
			}

			if err != nil {
				dialog.ShowError(fmt.Errorf("解压失败: %s", output), win)
				btn.Enable()
				return
			}

			// 解压成功，显示统一大小的对话框
			msgLabel := widget.NewLabel("文件已解压到:\n" + outputDir)
			msgLabel.Wrapping = fyne.TextWrapWord
			msgLabel.Alignment = fyne.TextAlignCenter

			// 直接包装 Label，不要使用 NewCenter，让 Label 填充整个宽度
			// 这样 TextWrapWord 才能根据 500px 宽度正常换行，而不是被 squeeze 成一列
			content := wrapWithMinSize(msgLabel)

			// 使用 Custom 对话框以保持与密码对话框一致的尺寸
			dialog.ShowCustom("完成", "确定", content, win)
			btn.Enable()
		})
	}()
}

func run7zzList(archivePath string, password string) (string, error) {
	args := []string{"l", "-slt", archivePath}
	if password != "" {
		args = append(args, "-p"+password)
	} else {
		args = append(args, "-p")
	}
	return run7zz(args...)
}

func run7zzExtract(archivePath string, outputDir string, password string) (string, error) {
	args := []string{"x", archivePath, "-y", "-o" + outputDir}
	if password != "" {
		args = append(args, "-p"+password)
	} else {
		args = append(args, "-p")
	}
	return run7zz(args...)
}

func run7zz(args ...string) (string, error) {
	cmd := exec.Command(sevenZipPath, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

func is7zzNotFound(err error) bool {
	var execErr *exec.Error
	if errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound) {
		return true
	}
	return false
}

func needsPassword(output string) bool {
	s := strings.ToLower(output)
	// 7zz 提示输入密码的常见文本
	if strings.Contains(s, "enter password") {
		return true
	}
	// 密码错误提示
	if strings.Contains(s, "wrong password") {
		return true
	}
	// 某些情况下的加密提示
	if strings.Contains(s, "encrypted") && strings.Contains(s, "password") {
		return true
	}
	// 无法打开文件作为归档，有时也是因为加密头导致无法识别
	// 但这可能也会误判损坏的文件，暂不启用
	// if strings.Contains(s, "cannot open the file as archive") { ... }

	return false
}

func detectArchiveSuffix(path string) string {
	name := strings.ToLower(filepath.Base(path))
	switch {
	case strings.HasSuffix(name, ".tar.gz"):
		return ".tar.gz"
	case strings.HasSuffix(name, ".tar.bz2"):
		return ".tar.bz2"
	case strings.HasSuffix(name, ".tar.xz"):
		return ".tar.xz"
	case strings.HasSuffix(name, ".tgz"):
		return ".tgz"
	case strings.HasSuffix(name, ".tbz2"):
		return ".tbz2"
	case strings.HasSuffix(name, ".txz"):
		return ".txz"
	}
	ext := strings.ToLower(filepath.Ext(name))
	if ext == "" {
		return "-"
	}
	return ext
}

func getResourcePath(name string) string {
	exePath, err := os.Executable()
	if err == nil {
		// 1. 检查 macOS App Bundle 资源目录: .../Contents/Resources/name
		// exePath 通常是 .../Contents/MacOS/executable
		appPath := filepath.Dir(filepath.Dir(exePath))
		resPath := filepath.Join(appPath, "Resources", name)
		if _, statErr := os.Stat(resPath); statErr == nil {
			return resPath
		}

		// 2. 检查可执行文件同级目录
		local := filepath.Join(filepath.Dir(exePath), name)
		if _, statErr := os.Stat(local); statErr == nil {
			return local
		}
	}

	// 3. 检查当前工作目录
	if _, statErr := os.Stat(name); statErr == nil {
		if abs, err := filepath.Abs(name); err == nil {
			return abs
		}
		return name
	}

	// 4. 返回原始名称，由调用者处理找不到的情况
	return name
}

func resolve7zzPath() string {
	return getResourcePath(SEVEN_ZZ_BASENAME)
}

func defaultOutputDir(archivePath string) string {
	parent := filepath.Dir(archivePath)
	base := filepath.Base(archivePath)
	suffix := detectArchiveSuffix(base)
	name := base
	if suffix != "-" && strings.HasSuffix(strings.ToLower(name), suffix) {
		name = name[:len(name)-len(suffix)]
	} else {
		ext := filepath.Ext(name)
		if ext != "" {
			name = name[:len(name)-len(ext)]
		}
	}
	if name == "" {
		name = "output"
	}
	return filepath.Join(parent, name)
}

func parse7zzListSlt(output string) []archiveItem {
	lines := strings.Split(output, "\n")
	items := make([]archiveItem, 0, 256)

	inItems := false
	var cur archiveItem
	hasCur := false

	flush := func() {
		if !hasCur {
			return
		}
		if cur.name != "" {
			items = append(items, cur)
		}
		cur = archiveItem{}
		hasCur = false
	}

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if line == "----------" {
			inItems = true
			continue
		}
		if !inItems {
			continue
		}

		parts := strings.SplitN(line, " = ", 2)
		if len(parts) != 2 {
			continue
		}
		key := parts[0]
		val := parts[1]

		switch key {
		case "Path":
			flush()
			hasCur = true
			cur.name = val
		case "Folder":
			if !hasCur {
				continue
			}
			if val == "+" {
				cur.isDir = true
			}
		case "Size":
			if !hasCur {
				continue
			}
			if v, err := strconv.ParseUint(val, 10, 64); err == nil {
				cur.size = v
			}
		case "Packed Size":
			if !hasCur {
				continue
			}
			if v, err := strconv.ParseUint(val, 10, 64); err == nil {
				cur.packed = v
			}
		case "Modified":
			if !hasCur {
				continue
			}
			// 去除毫秒部分
			if idx := strings.Index(val, "."); idx != -1 {
				val = val[:idx]
			}
			cur.modified = val
		case "Attributes":
			if !hasCur {
				continue
			}
			cur.attr = val
		}
	}
	flush()

	out := make([]archiveItem, 0, len(items))
	for _, it := range items {
		if it.name == "." {
			continue
		}
		out = append(out, it)
	}
	return out
}

func formatSize(v uint64) string {
	if v == 0 {
		return "0.00MB"
	}
	mb := float64(v) / 1024 / 1024
	return fmt.Sprintf("%.2fMB", mb)
}

type dropHintWidget struct {
	widget.BaseWidget
}

func newDropHint() *dropHintWidget {
	w := &dropHintWidget{}
	w.ExtendBaseWidget(w)
	return w
}

func (w *dropHintWidget) CreateRenderer() fyne.WidgetRenderer {
	text := canvas.NewText("请拖入压缩文件", nil)
	// 使用 hex 颜色解析或手动构造 color
	// 简单起见，这里直接解析 hex 颜色
	text.Color = parseHexColor(DROP_HINT_TEXT_COLOR)
	text.Alignment = fyne.TextAlignCenter
	text.TextStyle = fyne.TextStyle{Bold: true}
	text.TextSize = 22

	// 使用 SVG 生成虚线边框和加号
	// 注意: SVG 中的颜色需要使用 hex 字符串
	svgContent := `
<svg width="{w}" height="{h}" viewBox="0 0 {w} {h}" xmlns="http://www.w3.org/2000/svg">
  <rect x="2" y="2" width="{w_4}" height="{h_4}" rx="16" ry="16" fill="none" stroke="{color}" stroke-width="2" stroke-dasharray="8,8" />
  <path d="M{cx} {y1} V{y2} M{x1} {cy} H{x2}" stroke="{color}" stroke-width="3" stroke-linecap="round" />
</svg>`

	// 初始化时替换一次模板，确保 SVG 格式有效，避免 param mismatch 错误
	// 使用默认尺寸 200x100
	initSvg := svgContent
	initSvg = strings.ReplaceAll(initSvg, "{w}", "200")
	initSvg = strings.ReplaceAll(initSvg, "{h}", "100")
	initSvg = strings.ReplaceAll(initSvg, "{w_4}", "196")
	initSvg = strings.ReplaceAll(initSvg, "{h_4}", "96")
	initSvg = strings.ReplaceAll(initSvg, "{cx}", "100")
	initSvg = strings.ReplaceAll(initSvg, "{cy}", "50")
	initSvg = strings.ReplaceAll(initSvg, "{x1}", "85")
	initSvg = strings.ReplaceAll(initSvg, "{x2}", "115")
	initSvg = strings.ReplaceAll(initSvg, "{y1}", "35")
	initSvg = strings.ReplaceAll(initSvg, "{y2}", "65")
	initSvg = strings.ReplaceAll(initSvg, "{color}", DROP_HINT_BORDER_COLOR)

	// 使用 NewStaticResource 而不是 NewReader，避免潜在的解析问题
	res := fyne.NewStaticResource("drop-hint-init.svg", []byte(initSvg))
	img := canvas.NewImageFromResource(res)
	img.FillMode = canvas.ImageFillStretch

	objs := []fyne.CanvasObject{img, text}

	return &dropHintRenderer{
		widget: w,
		text:   text,
		img:    img,
		objs:   objs,
		svgTpl: svgContent,
	}
}

type dropHintRenderer struct {
	widget *dropHintWidget
	text   *canvas.Text
	img    *canvas.Image
	objs   []fyne.CanvasObject
	svgTpl string
}

func (r *dropHintRenderer) Layout(size fyne.Size) {
	// 使用 layout 包辅助居中
	// 或者手动计算
	// 文本稍微下移一点，给加号腾出空间
	r.text.Resize(fyne.NewSize(size.Width, r.text.MinSize().Height))
	r.text.Move(fyne.NewPos(0, size.Height/2+30))

	padding := DROP_HINT_PADDING
	imgSize := fyne.NewSize(size.Width-2*padding, size.Height-2*padding)
	r.img.Resize(imgSize)
	r.img.Move(fyne.NewPos(padding, padding))

	// 更新 SVG
	w := imgSize.Width
	h := imgSize.Height
	if w <= 0 || h <= 0 {
		return
	}

	// 计算中心点和加号坐标
	cx := w / 2
	cy := h / 2
	plusSize := float32(40) // 加号大小
	half := plusSize / 2

	// 简单的模板替换
	s := r.svgTpl
	s = strings.ReplaceAll(s, "{w}", fmt.Sprintf("%f", w))
	s = strings.ReplaceAll(s, "{h}", fmt.Sprintf("%f", h))
	s = strings.ReplaceAll(s, "{w_4}", fmt.Sprintf("%f", w-4))
	s = strings.ReplaceAll(s, "{h_4}", fmt.Sprintf("%f", h-4))
	s = strings.ReplaceAll(s, "{cx}", fmt.Sprintf("%f", cx))
	s = strings.ReplaceAll(s, "{cy}", fmt.Sprintf("%f", cy))
	s = strings.ReplaceAll(s, "{x1}", fmt.Sprintf("%f", cx-half))
	s = strings.ReplaceAll(s, "{x2}", fmt.Sprintf("%f", cx+half))
	s = strings.ReplaceAll(s, "{y1}", fmt.Sprintf("%f", cy-half))
	s = strings.ReplaceAll(s, "{y2}", fmt.Sprintf("%f", cy+half))
	s = strings.ReplaceAll(s, "{color}", DROP_HINT_BORDER_COLOR)

	// 生成唯一的资源名称，避免 Fyne 缓存旧尺寸的 SVG导致渲染异常(如圆角变大、加号变大)
	resName := fmt.Sprintf("drop-hint-%d-%d.svg", int(w), int(h))
	res := fyne.NewStaticResource(resName, []byte(s))
	r.img.Resource = res
	r.img.Refresh()
}

func (r *dropHintRenderer) MinSize() fyne.Size { return fyne.NewSize(200, 120) }
func (r *dropHintRenderer) Refresh() {
	r.text.Refresh()
	r.img.Refresh()
}
func (r *dropHintRenderer) Destroy()                     {}
func (r *dropHintRenderer) Objects() []fyne.CanvasObject { return r.objs }

// 辅助函数：解析 hex 颜色
func parseHexColor(s string) color.Color {
	c := color.NRGBA{R: 0, G: 0, B: 0, A: 255}
	if len(s) != 7 || s[0] != '#' {
		return c
	}
	hexToByte := func(b byte) byte {
		switch {
		case b >= '0' && b <= '9':
			return b - '0'
		case b >= 'a' && b <= 'f':
			return b - 'a' + 10
		case b >= 'A' && b <= 'F':
			return b - 'A' + 10
		}
		return 0
	}
	c.R = hexToByte(s[1])<<4 + hexToByte(s[2])
	c.G = hexToByte(s[3])<<4 + hexToByte(s[4])
	c.B = hexToByte(s[5])<<4 + hexToByte(s[6])
	return c
}
