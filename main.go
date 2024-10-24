package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nsf/termbox-go"
)

type SpotifyDisplay struct {
	bus             *dbus.Conn
	spotifyObject   dbus.BusObject
	cacheDir        string
	lastMetadata    map[string]interface{}
	minWidth        int
	contentHeight   int
	margin          int
	horizontalAlign string // "left", "center", "right"
	verticalAlign   string // "top", "center", "bottom"
	currentArtURL   string
}

type TerminalSize struct {
	width  int
	height int
	startX int
	startY int
}

type Metadata struct {
	Title    string
	Artist   string
	Album    string
	Length   int64
	Position int64
	ArtURL   string
}

func NewSpotifyDisplay() (*SpotifyDisplay, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %v", err)
	}

	cacheDir := filepath.Join(homeDir, ".cache", "spotify-display")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %v", err)
	}

	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to session bus: %v", err)
	}

	obj := conn.Object("org.mpris.MediaPlayer2.spotify", "/org/mpris/MediaPlayer2")

	return &SpotifyDisplay{
		bus:             conn,
		spotifyObject:   obj,
		cacheDir:        cacheDir,
		minWidth:        60,
		contentHeight:   9,
		margin:          2,
		horizontalAlign: "center",
		verticalAlign:   "bottom",
	}, nil
}

func (sd *SpotifyDisplay) getTerminalSize() TerminalSize {
	width, height := termbox.Size()

	var startX, startY int

	switch sd.horizontalAlign {
	case "left":
		startX = sd.margin
	case "right":
		startX = width - sd.minWidth - sd.margin
	default: // center
		startX = (width - sd.minWidth) / 2
	}

	switch sd.verticalAlign {
	case "top":
		startY = sd.margin
	case "bottom":
		startY = height - sd.contentHeight - sd.margin
	default: // center
		startY = (height - sd.contentHeight) / 2
	}

	return TerminalSize{
		width:  width,
		height: height,
		startX: startX,
		startY: startY,
	}
}

func (sd *SpotifyDisplay) getMetadata() (*Metadata, error) {
	variant, err := sd.spotifyObject.GetProperty("org.mpris.MediaPlayer2.Player.Metadata")
	if err != nil {
		return nil, err
	}

	metadata, ok := variant.Value().(map[string]dbus.Variant)
	if !ok {
		return nil, fmt.Errorf("invalid metadata format")
	}

	position, err := sd.spotifyObject.GetProperty("org.mpris.MediaPlayer2.Player.Position")
	if err != nil {
		return nil, err
	}

	var length int64
	switch v := metadata["mpris:length"].Value().(type) {
	case int64:
		length = v
	case uint64:
		length = int64(v)
	default:
		return nil, fmt.Errorf("unexpected length type: %T", v)
	}

	var pos int64
	switch v := position.Value().(type) {
	case int64:
		pos = v
	case uint64:
		pos = int64(v)
	default:
		return nil, fmt.Errorf("unexpected position type: %T", v)
	}

	artists, ok := metadata["xesam:artist"].Value().([]string)
	if !ok {
		return nil, fmt.Errorf("invalid artist format")
	}

	artistName := "Unknown Artist"
	if len(artists) > 0 {
		artistName = artists[0]
	}

	// Get and parse art URL with debug logging
	artURL := ""
	if artURLVar, ok := metadata["mpris:artUrl"]; ok {
		rawURL := artURLVar.String()
		// Remove quotes if present
		rawURL = strings.Trim(rawURL, "\"")
		log.Printf("Raw art URL: %s", rawURL)

		switch {
		case strings.HasPrefix(rawURL, "https://i.scdn.co/image/"):
			artURL = rawURL // Use the full URL as is
			log.Printf("Using Spotify CDN URL: %s", artURL)
		case strings.HasPrefix(rawURL, "file://"):
			localPath := strings.TrimPrefix(rawURL, "file://")
			artURL = localPath
			log.Printf("Using local file path: %s", artURL)
		default:
			log.Printf("Unknown URL format: %s", rawURL)
		}
	}

	return &Metadata{
		Title:    metadata["xesam:title"].String(),
		Artist:   artistName,
		Album:    metadata["xesam:album"].String(),
		Length:   length / 1000000,
		Position: pos / 1000000,
		ArtURL:   artURL,
	}, nil
}

func (sd *SpotifyDisplay) downloadArtwork(artURL string) (string, error) {
	if artURL == "" {
		return "", fmt.Errorf("no artwork URL provided")
	}

	log.Printf("Downloading artwork from: %s", artURL)
	imagePath := filepath.Join(sd.cacheDir, "current_artwork.png")

	// Handle local file
	if strings.HasPrefix(artURL, "/") {
		log.Printf("Handling local file: %s", artURL)
		input, err := os.Open(artURL)
		if err != nil {
			return "", fmt.Errorf("failed to open local artwork: %v", err)
		}
		defer input.Close()

		output, err := os.Create(imagePath)
		if err != nil {
			return "", fmt.Errorf("failed to create artwork file: %v", err)
		}
		defer output.Close()

		_, err = io.Copy(output, input)
		if err != nil {
			return "", fmt.Errorf("failed to copy artwork: %v", err)
		}

		return imagePath, nil
	}

	// Handle remote URL
	log.Printf("Handling remote URL: %s", artURL)
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	req, err := http.NewRequest("GET", artURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %v", err)
	}

	// Add User-Agent header to avoid potential 403 errors
	req.Header.Set("User-Agent", "spotify-display/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download artwork: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download artwork: HTTP %d", resp.StatusCode)
	}

	out, err := os.Create(imagePath)
	if err != nil {
		return "", fmt.Errorf("failed to create artwork file: %v", err)
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to save artwork: %v", err)
	}

	log.Printf("Successfully downloaded artwork to: %s", imagePath)
	return imagePath, nil
}

// Only modifying the displayImage function to fix the aspect ratio issue

func (sd *SpotifyDisplay) displayImage(imagePath string, startX, startY int) error {
	if imagePath == "" {
		return fmt.Errorf("no image path provided")
	}

	// Check if the file exists and is not empty
	info, err := os.Stat(imagePath)
	if err != nil {
		return fmt.Errorf("artwork file error: %v", err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("artwork file is empty")
	}

	// Check if chafa is available
	chafaPath, err := exec.LookPath("chafa")
	if err != nil {
		return fmt.Errorf("chafa is not installed")
	}
	log.Printf("Found chafa at: %s", chafaPath)

	// Save current cursor position
	fmt.Print("\0337")

	// Move cursor to image position
	moveCursor(startX, startY)

	// Run chafa with specific options for terminal compatibility
	cmd := exec.Command(chafaPath,
		"--size=18x18",
		"--symbols=block",
		"--colors=256",
		imagePath)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Run()
	if err != nil {
		log.Printf("Chafa error: %v", err)
	}

	// Restore cursor position
	fmt.Print("\0338")

	return err
}

func formatTime(seconds int64) string {
	minutes := seconds / 60
	remainingSeconds := seconds % 60
	return fmt.Sprintf("%02d:%02d", minutes, remainingSeconds)
}

func (sd *SpotifyDisplay) drawProgressBar(metadata *Metadata, term TerminalSize) {
	width := 40
	var progress int
	if metadata.Length > 0 {
		progress = int(float64(metadata.Position) / float64(metadata.Length) * float64(width))
		if progress < 0 {
			progress = 0
		}
		if progress > width {
			progress = width
		}
	}

	bar := strings.Repeat("━", progress) + strings.Repeat("─", width-progress)
	currentTime := formatTime(metadata.Position)
	totalTime := formatTime(metadata.Length)
	timeText := fmt.Sprintf("%s/%s", currentTime, totalTime)

	// Clear previous progress area
	moveCursor(term.startX+20, term.startY+4)
	fmt.Print(strings.Repeat(" ", 60))
	moveCursor(term.startX+20, term.startY+5)
	fmt.Print(strings.Repeat(" ", 60))

	// Draw progress bar and time
	moveCursor(term.startX+20, term.startY+4)
	fmt.Print(bar)
	moveCursor(term.startX+20+(width-len(timeText))/2, term.startY+5)
	fmt.Print(timeText)
}

func (sd *SpotifyDisplay) handleKeyboard(event termbox.Event) bool {
	redraw := false

	switch event.Key {
	case termbox.KeyArrowUp:
		sd.verticalAlign = "top"
		redraw = true
	case termbox.KeyArrowDown:
		sd.verticalAlign = "bottom"
		redraw = true
	case termbox.KeyArrowLeft:
		sd.horizontalAlign = "left"
		redraw = true
	case termbox.KeyArrowRight:
		sd.horizontalAlign = "right"
		redraw = true
	}

	if event.Ch == 'c' {
		sd.horizontalAlign = "center"
		sd.verticalAlign = "center"
		redraw = true
	}

	return redraw
}

func clearScreen() {
	fmt.Print("\033[2J\033[H")
}

func moveCursor(x, y int) {
	fmt.Printf("\033[%d;%dH", y+1, x+1)
}

func hideCursor() {
	fmt.Print("\033[?25l")
}

func showCursor() {
	fmt.Print("\033[?25h")
}

func (sd *SpotifyDisplay) Run() error {
	if err := termbox.Init(); err != nil {
		return fmt.Errorf("failed to initialize termbox: %v", err)
	}
	defer termbox.Close()

	hideCursor()
	defer showCursor()
	clearScreen()

	// Create a log file
	logFile, err := os.OpenFile("spotify-display.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %v", err)
	}
	defer logFile.Close()
	log.SetOutput(logFile)

	eventQueue := make(chan termbox.Event)
	go func() {
		for {
			eventQueue <- termbox.PollEvent()
		}
	}()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case event := <-eventQueue:
			if event.Type == termbox.EventKey {
				if event.Ch == 'q' {
					return nil
				}
				if sd.handleKeyboard(event) {
					clearScreen()
					sd.currentArtURL = ""
				}
			}

		case <-ticker.C:
			term := sd.getTerminalSize()
			metadata, err := sd.getMetadata()
			if err != nil {
				log.Printf("Error getting metadata: %v", err)
				continue
			}

			// Update track info
			moveCursor(term.startX+20, term.startY)
			fmt.Print("♫ Now Playing")
			moveCursor(term.startX+20, term.startY+1)
			fmt.Print(metadata.Title)
			moveCursor(term.startX+20, term.startY+2)
			fmt.Printf("by %s", metadata.Artist)

			// Update progress bar
			sd.drawProgressBar(metadata, term)

			// Update artwork if changed
			if metadata.ArtURL != sd.currentArtURL && metadata.ArtURL != "" {
				sd.currentArtURL = metadata.ArtURL
				imagePath, err := sd.downloadArtwork(metadata.ArtURL)
				if err != nil {
					log.Printf("Artwork error: %v", err)
				} else if imagePath != "" {
					if err := sd.displayImage(imagePath, term.startX, term.startY); err != nil {
						log.Printf("Display error: %v", err)
					}
				}
			}

		case <-sigChan:
			return nil
		}
	}
}

func main() {
	// Check if Spotify is running
	cmd := exec.Command("pgrep", "spotify")
	if err := cmd.Run(); err != nil {
		fmt.Println("Spotify is not running. Please start Spotify first.")
		return
	}

	display, err := NewSpotifyDisplay()
	if err != nil {
		log.Fatalf("Failed to initialize display: %v", err)
	}

	if err := display.Run(); err != nil {
		log.Fatalf("Display error: %v", err)
	}

	clearScreen()
	showCursor()
}
