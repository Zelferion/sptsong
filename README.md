# 🎵 sptsong 

A terminal-based Spotify song display written in Go that shows your currently playing track with album art.



## 🚀 Installation

```bash
# Clone the repository
git clone https://github.com/Zelferion/sptsong.git

# Navigate to the directory
cd sptsong

# Install dependencies
go mod init sptsong
go mod tidy

# Build the binary
go build -o sptsong main.go

# Make it executable from everywhere
# If you want to use it for windows figure out yourself where to put the binary
sudo mv ./sptsong /usr/local/bin
```

### Prerequisites

- Go 1.16 or higher
- DBus
- Chafa (for image rendering)
- Active Spotify session

## 🎮 Usage

```bash
# Run the program
sptsong
```

### Controls

- `↑` `↓` `←` `→` - Move display position
- `c` - Center display
- `q` - Quit

## 🛠️ Technical Details

The application uses:
- DBus for Spotify integration
- termbox-go for terminal manipulation
- Chafa for image rendering

## ⚙️ Configuration

Display settings can be adjusted through the terminal interface or by modifying the config values in the source code.

## 📝 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## 🙏 Acknowledgments

- Spotify DBus interface
- [Chafa](https://hpjansson.org/chafa/) for terminal graphics
- [termbox-go](https://github.com/nsf/termbox-go) for terminal handling

---
Made with ❤️ by [Zelferion](https://github.com/Zelferion)1
