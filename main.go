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

type Config struct {
	minWidth        int
	contentHeight   int
	margin          int
	horizontalAlign string
	verticalAlign   string
}

type SpotifyDisplay struct {
	bus           *dbus.Conn
	spotifyObject dbus.BusObject
	cacheDir      string
	currentArtURL string
	Config
}

type Metadata struct {
	Title    string
	Artist   string
	Length   int64
	Position int64
	ArtURL   string
}

type TerminalSize struct {
	width, height, startX, startY int
}

func NewSpotifyDisplay() (*SpotifyDisplay, error) {
	homeDir, _ := os.UserHomeDir()
	cacheDir := filepath.Join(homeDir, ".cache", "spotify-display")
	os.MkdirAll(cacheDir, 0o755)

	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, err
	}

	return &SpotifyDisplay{
		bus:           conn,
		spotifyObject: conn.Object("org.mpris.MediaPlayer2.spotify", "/org/mpris/MediaPlayer2"),
		cacheDir:      cacheDir,
		Config: Config{
			minWidth:        60,
			contentHeight:   9,
			margin:          2,
			horizontalAlign: "center",
			verticalAlign:   "bottom",
		},
	}, nil
}

func (sd *SpotifyDisplay) getTerminalSize() TerminalSize {
	width, height := termbox.Size()
	startX := (width - sd.minWidth) / 2
	startY := height - sd.contentHeight - sd.margin

	if sd.horizontalAlign == "left" {
		startX = sd.margin
	} else if sd.horizontalAlign == "right" {
		startX = width - sd.minWidth - sd.margin
	}

	if sd.verticalAlign == "top" {
		startY = sd.margin
	} else if sd.verticalAlign == "center" {
		startY = (height - sd.contentHeight) / 2
	}

	return TerminalSize{width, height, startX, startY}
}

func (sd *SpotifyDisplay) getMetadata() (*Metadata, error) {
	variant, err := sd.spotifyObject.GetProperty("org.mpris.MediaPlayer2.Player.Metadata")
	if err != nil {
		return nil, err
	}

	metadata := variant.Value().(map[string]dbus.Variant)
	position, _ := sd.spotifyObject.GetProperty("org.mpris.MediaPlayer2.Player.Position")

	artists := metadata["xesam:artist"].Value().([]string)
	artist := "Unknown Artist"
	if len(artists) > 0 {
		artist = artists[0]
	}

	rawURL := strings.Trim(metadata["mpris:artUrl"].String(), "\"")
	artURL := ""
	if strings.HasPrefix(rawURL, "https://i.scdn.co/image/") {
		artURL = rawURL
	} else if strings.HasPrefix(rawURL, "file://") {
		artURL = strings.TrimPrefix(rawURL, "file://")
	}

	var length int64
	switch v := metadata["mpris:length"].Value().(type) {
	case int64:
		length = v
	case uint64:
		length = int64(v)
	}

	var pos int64
	switch v := position.Value().(type) {
	case int64:
		pos = v
	case uint64:
		pos = int64(v)
	}

	return &Metadata{
		Title:    metadata["xesam:title"].String(),
		Artist:   artist,
		Length:   length / 1000000,
		Position: pos / 1000000,
		ArtURL:   artURL,
	}, nil
}

func (sd *SpotifyDisplay) downloadArtwork(artURL string) (string, error) {
	if artURL == "" {
		return "", nil
	}

	imagePath := filepath.Join(sd.cacheDir, "current_artwork.png")
	var input io.ReadCloser
	var err error

	if strings.HasPrefix(artURL, "/") {
		input, err = os.Open(artURL)
	} else {
		req, _ := http.NewRequest("GET", artURL, nil)
		req.Header.Set("User-Agent", "spotify-display/1.0")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", err
		}
		input = resp.Body
	}
	defer input.Close()

	output, err := os.Create(imagePath)
	if err != nil {
		return "", err
	}
	defer output.Close()

	_, err = io.Copy(output, input)
	return imagePath, err
}

func (sd *SpotifyDisplay) displayImage(imagePath string, startX, startY int) error {
	chafaPath, err := exec.LookPath("chafa")
	if err != nil {
		return err
	}

	fmt.Print("\0337")
	fmt.Printf("\033[%d;%dH", startY+1, startX+1)

	cmd := exec.Command(chafaPath, "--size=18x18", "--symbols=block", "--colors=256", imagePath)
	cmd.Stdout = os.Stdout
	cmd.Run()

	fmt.Print("\0338")
	return nil
}

func (sd *SpotifyDisplay) drawProgressBar(metadata *Metadata, term TerminalSize) {
	width := 40
	progress := int(float64(metadata.Position) / float64(metadata.Length) * float64(width))
	if progress < 0 {
		progress = 0
	} else if progress > width {
		progress = width
	}

	bar := strings.Repeat("━", progress) + strings.Repeat("─", width-progress)
	timeText := fmt.Sprintf("%02d:%02d/%02d:%02d",
		metadata.Position/60, metadata.Position%60,
		metadata.Length/60, metadata.Length%60)

	fmt.Printf("\033[%d;%dH%s", term.startY+5, term.startX+20, strings.Repeat(" ", 60))
	fmt.Printf("\033[%d;%dH%s", term.startY+6, term.startX+20, strings.Repeat(" ", 60))
	fmt.Printf("\033[%d;%dH%s", term.startY+5, term.startX+20, bar)
	fmt.Printf("\033[%d;%dH%s", term.startY+6, term.startX+20+(width-len(timeText))/2, timeText)
}

func (sd *SpotifyDisplay) handleKeyboard(event termbox.Event) bool {
	switch event.Key {
	case termbox.KeyArrowUp:
		sd.verticalAlign = "top"
	case termbox.KeyArrowDown:
		sd.verticalAlign = "bottom"
	case termbox.KeyArrowLeft:
		sd.horizontalAlign = "left"
	case termbox.KeyArrowRight:
		sd.horizontalAlign = "right"
	default:
		if event.Ch == 'c' {
			sd.horizontalAlign = "center"
			sd.verticalAlign = "center"
		} else {
			return false
		}
	}
	return true
}

func (sd *SpotifyDisplay) Run() error {
	if err := termbox.Init(); err != nil {
		return err
	}
	defer termbox.Close()

	fmt.Print("\033[?25l")
	defer fmt.Print("\033[?25h")
	fmt.Print("\033[2J\033[H")

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
					fmt.Print("\033[2J\033[H")
					sd.currentArtURL = ""
				}
			}

		case <-ticker.C:
			term := sd.getTerminalSize()
			metadata, err := sd.getMetadata()
			if err != nil {
				continue
			}

			fmt.Printf("\033[%d;%dH♫ Now Playing", term.startY+1, term.startX+20)
			fmt.Printf("\033[%d;%dH%s", term.startY+2, term.startX+20, metadata.Title)
			fmt.Printf("\033[%d;%dHby %s", term.startY+3, term.startX+20, metadata.Artist)
			sd.drawProgressBar(metadata, term)

			if metadata.ArtURL != sd.currentArtURL && metadata.ArtURL != "" {
				sd.currentArtURL = metadata.ArtURL
				if imagePath, err := sd.downloadArtwork(metadata.ArtURL); err == nil {
					sd.displayImage(imagePath, term.startX, term.startY)
				}
			}

		case <-sigChan:
			return nil
		}
	}
}

func main() {
	if err := exec.Command("pgrep", "spotify").Run(); err != nil {
		fmt.Println("Spotify is not running. Please start Spotify first.")
		return
	}

	display, err := NewSpotifyDisplay()
	if err != nil {
		log.Fatal(err)
	}

	if err := display.Run(); err != nil {
		log.Fatal(err)
	}

	fmt.Print("\033[2J\033[H")
	fmt.Print("\033[?25h")
}

