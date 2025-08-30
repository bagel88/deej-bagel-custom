# Deej - Custom edit for Linux with Enhanced Features

This is a custom build of [Deej](https://github.com/omriharel/deej) with enhanced features, specifically designed for modern Linux systems.

## 🚀 Features

### Original Deej Features
- **Hardware Volume Control**: Control your PC's volume using physical sliders
- **Multi-Application Support**: Control volume for individual applications
- **Cross-Platform**: Works on Windows and Linux (But most changes are for Linux)
- **Real-time Control**: Instant volume changes with physical feedback

### Custom Enhancements
- **Serial Error Recovery**: Automatic reconnection when serial connection is lost
- **WINE/Proton Compatibility**: Handles USB/serial disruptions when launching WINE/Proton games
- **Application Resilience**: Panic recovery and automatic restart capabilities
- **Enhanced Logging**: Better error reporting and debugging information
- **Smart Session Management**: Configurable session refresh intervals and fast detection
- **Performance Optimizations**: Reduced unnecessary session scans and improved efficiency
- **Advanced Configuration**: Extended config options for fine-tuning behavior

## 🔧 What's Fixed

This build addresses the common issue where Deej crashes with EOF errors when:
- Starting games through WINE/Proton
- USB connection becomes temporarily unstable
- Serial port gets disconnected and reconnected

**Before**: Application crashes with `Failed to read line from serial {"error": "EOF"}`
**After**: Application automatically recovers and reconnects

**Before**: Frequent session scanning causing performance issues
**After**: Configurable session refresh intervals with smart detection

**Before**: Fixed session refresh intervals that couldn't be tuned
**After**: Flexible configuration for different use cases and performance needs

## 📦 Installation

### Prerequisites
- Go 1.16 or later
- Arduino IDE (for hardware setup)
- Physical slider hardware (see [original Deej documentation](https://github.com/omriharel/deej))

### Building from Source

1. Clone this repository:
```bash
git clone <your-repo-url>
cd deej-bagel-custom
```

2. Build the application:
```bash
# Development build (with debug symbols)
chmod +x pkg/deej/scripts/linux/build-dev.sh
./pkg/deej/scripts/linux/build-dev.sh

# Release build (optimized)
chmod +x pkg/deej/scripts/linux/build-release.sh
./pkg/deej/scripts/linux/build-release.sh
```

3. Run the application:
```bash
# For testing/development
./deej-dev --verbose

# For production use
./deej-release
```

## 🎮 WINE Gaming Support

This build is specifically tested and optimized for Linux gaming with WINE. When you launch a game through WINE, the application will:

1. Detect the serial connection disruption
2. Log the error without crashing
3. Automatically attempt to reconnect
4. Resume normal operation once the connection is restored

## ⚡ Performance & Session Management

### Smart Session Detection
This build includes intelligent session management that significantly improves performance:

- **Configurable Refresh Intervals**: Set how often Deej scans for new audio sessions
- **Fast Detection Mode**: Enable faster session detection for gaming scenarios
- **Performance Optimization**: Reduced unnecessary session scans
- **Flexible Configuration**: Tune behavior for different use cases

### Use Cases
- **Gaming**: Use `fast_session_detection: true` and lower `session_refresh_interval` for quick game detection
- **Productivity**: Use higher intervals for better performance during work
- **Custom Workflows**: Disable automatic refresh with `session_refresh_interval: 0` for manual control

## 🔍 Technical Details

### Serial Error Recovery Implementation

The fix implements a robust error handling system:

1. **Error Channel**: Serial errors are captured and sent to a dedicated error channel
2. **Automatic Recovery**: A background goroutine handles connection recovery
3. **Graceful Degradation**: Application continues running during connection issues
4. **Retry Logic**: Intelligent retry with exponential backoff

### Key Files Modified

- `pkg/deej/serial.go`: Added serial error channel and recovery handler
- `pkg/deej/cmd/main.go`: Added panic recovery and restart loop
- `pkg/deej/session_map.go`: Added configurable session refresh intervals and smart detection
- `pkg/deej/config.go`: Added new configuration options for session management and performance tuning
- `pkg/deej/session_finder_linux.go`: **TO BE REPLACED** with PipeWire implementation
- `pkg/deej/session_linux.go`: **TO BE UPDATED** for PipeWire session handling

## 📝 Configuration

Create a `config.yaml` file in the same directory as the executable:

```yaml
# Serial connection settings
com_port: "/dev/ttyUSB0"  # Your serial port
baud_rate: 9600

# Slider behavior
invert_sliders: false               # if you want slider inverted or not
noise_reduction_level: default      # default, low, high

# Session management (NEW!)
session_refresh_interval: 30        # How often to refresh sessions (seconds, 0 = disabled)
max_session_refresh_interval: 45    # Maximum time between forced refreshes (seconds)
fast_session_detection: false       # Enable faster session detection (halves the interval)

# Performance settings
periodic_update_interval: 10        # How often to update volumes (seconds)
detect_external_changes: true       # Detect volume changes from other applications

# Slider mappings
slider_mapping:
  0: master
  1: discord
  2: spotify
  3:
    - wine64-preloader
    - game2
  4: firefox
```

### New Configuration Options

#### Session Management
- **`session_refresh_interval`**: Controls how often Deej scans for new audio sessions (default: 30 seconds)
  - Set to `0` to disable automatic session refresh
  - Lower values = more responsive to new applications
  - Higher values = better performance
- **`max_session_refresh_interval`**: Maximum time between forced session refreshes (default: 45 seconds)
- **`fast_session_detection`**: When enabled, reduces the refresh interval by half for faster detection

#### Performance Tuning
- **`periodic_update_interval`**: How often volume updates are applied (default: 10 seconds)
- **`detect_external_changes`**: Whether to detect volume changes from other applications (default: true)

## 🤝 Contributing

This is a fork of the original Deej project. If you find issues or want to contribute:

1. Check if the issue exists in the [original Deej repository](https://github.com/omriharel/deej)
2. For issues specific to this build, open an issue in this repository
3. Submit pull requests for improvements

## 📄 License

This project is based on the original Deej project. See the [original LICENSE](https://github.com/omriharel/deej/blob/master/LICENSE) for details.

## 🙏 Acknowledgments

- Original Deej project by [omriharel](https://github.com/omriharel)
- Serial error recovery fix inspired by [PR #150](https://github.com/omriharel/deej/pull/150)
- Session management improvements and performance optimizations developed collaboratively
- PipeWire integration for modern Linux audio support (Still TODO, 2026)

## 🔗 Links

- [Original Deej Repository](https://github.com/omriharel/deej)
- [Deej Documentation](https://github.com/omriharel/deej#readme)
- [Hardware Setup Guide](https://github.com/omriharel/deej#hardware)
