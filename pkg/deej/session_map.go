package deej

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/omriharel/deej/pkg/deej/util"
	"github.com/thoas/go-funk"
	"go.uber.org/zap"
)

type sessionMap struct {
	deej   *Deej
	logger *zap.SugaredLogger

	m    map[string][]Session
	lock sync.Locker

	sessionFinder SessionFinder

	lastSessionRefresh time.Time
	unmappedSessions   []Session
}

const (
	masterSessionName = "master" // master device volume
	systemSessionName = "system" // system sounds volume
	inputSessionName  = "mic"    // microphone input level

	// some targets need to be transformed before their correct audio sessions can be accessed.
	// this prefix identifies those targets to ensure they don't contradict with another similarly-named process
	specialTargetTransformPrefix = "deej."

	// targets the currently active window (Windows-only, experimental)
	specialTargetCurrentWindow = "current"

	// targets all currently unmapped sessions (experimental)
	specialTargetAllUnmapped = "unmapped"
)

// this matches friendly device names (on Windows), e.g. "Headphones (Realtek Audio)"
var deviceSessionKeyPattern = regexp.MustCompile(`^.+ \(.+\)$`)

func newSessionMap(deej *Deej, logger *zap.SugaredLogger, sessionFinder SessionFinder) (*sessionMap, error) {
	logger = logger.Named("sessions")

	m := &sessionMap{
		deej:          deej,
		logger:        logger,
		m:             make(map[string][]Session),
		lock:          &sync.Mutex{},
		sessionFinder: sessionFinder,
	}

	logger.Debug("Created session map instance")

	return m, nil
}

func (m *sessionMap) initialize() error {
	if err := m.getAndAddSessions(); err != nil {
		m.logger.Warnw("Failed to get all sessions during session map initialization", "error", err)
		return fmt.Errorf("get all sessions during init: %w", err)
	}

	m.setupOnConfigReload()
	m.setupOnSliderMove()
	m.setupPeriodicVolumeUpdates()
	m.setupAutomaticSessionRefresh()

	return nil
}

func (m *sessionMap) release() error {
	if err := m.sessionFinder.Release(); err != nil {
		m.logger.Warnw("Failed to release session finder during session map release", "error", err)
		return fmt.Errorf("release session finder during release: %w", err)
	}

	return nil
}

// assumes the session map is clean!
// only call on a new session map or as part of refreshSessions which calls reset
func (m *sessionMap) getAndAddSessions() error {

	// mark that we're refreshing before anything else
	m.lastSessionRefresh = time.Now()
	m.unmappedSessions = nil

	sessions, err := m.sessionFinder.GetAllSessions()
	if err != nil {
		m.logger.Warnw("Failed to get sessions from session finder", "error", err)
		return fmt.Errorf("get sessions from SessionFinder: %w", err)
	}

	for _, session := range sessions {
		m.add(session)

		if !m.sessionMapped(session) {
			m.logger.Debugw("Tracking unmapped session", "session", session)
			m.unmappedSessions = append(m.unmappedSessions, session)
		}
	}

	m.logger.Infow("Got all audio sessions successfully", "sessionMap", m)

	return nil
}

func (m *sessionMap) setupOnConfigReload() {
	configReloadedChannel := m.deej.config.SubscribeToChanges()

	go func() {
		for {
			select {
			case <-configReloadedChannel:
				m.logger.Info("Detected config reload, attempting to re-acquire all audio sessions")
				m.refreshSessions(false)
			}
		}
	}()
}

func (m *sessionMap) setupOnSliderMove() {
	sliderEventsChannel := m.deej.serial.SubscribeToSliderMoveEvents()

	go func() {
		for {
			select {
			case event := <-sliderEventsChannel:
				m.handleSliderMoveEvent(event)
			}
		}
	}()
}

func (m *sessionMap) setupPeriodicVolumeUpdates() {
	// Check if periodic updates are enabled (interval > 0)
	if m.deej.config.PeriodicUpdateInterval <= 0 {
		m.logger.Debug("Periodic volume updates disabled (interval set to 0)")
		return
	}

	interval := time.Duration(m.deej.config.PeriodicUpdateInterval) * time.Second
	ticker := time.NewTicker(interval)

	m.logger.Infow("Setting up periodic volume updates", "interval", interval)

	go func() {
		for {
			select {
			case <-ticker.C:
				m.handlePeriodicVolumeUpdate()
			}
		}
	}()
}

func (m *sessionMap) setupAutomaticSessionRefresh() {
	m.logger.Debugw("Setting up automatic session refresh",
		"SessionRefreshInterval", m.deej.config.SessionRefreshInterval,
		"FastSessionDetection", m.deej.config.FastSessionDetection)

	// Check if automatic session refresh is enabled (interval > 0)
	if m.deej.config.SessionRefreshInterval <= 0 {
		m.logger.Debug("Automatic session refresh disabled (interval set to 0)")
		return
	}

	interval := time.Duration(m.deej.config.SessionRefreshInterval) * time.Second

	// Apply fast detection if enabled (reduce interval by half)
	if m.deej.config.FastSessionDetection {
		interval = interval / 2
		m.logger.Infow("Fast session detection enabled, using reduced interval", "original", m.deej.config.SessionRefreshInterval, "reduced", interval)
	}

	ticker := time.NewTicker(interval)

	m.logger.Infow("Setting up automatic session refresh", "interval", interval)

	go func() {
		for {
			select {
			case <-ticker.C:
				m.logger.Debugw("Performing scheduled session refresh",
					"interval", interval,
					"timeSinceLast", time.Since(m.lastSessionRefresh))
				m.refreshSessions(false)
			}
		}
	}()
}

// performance: explain why force == true at every such use to avoid unintended forced refresh spams
func (m *sessionMap) refreshSessions(force bool) {

	// Get the minimum time between refreshes from config, with fallback to default
	minTimeBetweenRefreshes := time.Duration(m.deej.config.SessionRefreshInterval) * time.Second
	if minTimeBetweenRefreshes <= 0 {
		// Use default if not configured or disabled
		minTimeBetweenRefreshes = time.Second * 5
	}

	// Apply fast detection if enabled (reduce interval by half) - same logic as in setupAutomaticSessionRefresh
	if m.deej.config.FastSessionDetection {
		minTimeBetweenRefreshes = minTimeBetweenRefreshes / 2
	}

	// make sure enough time passed since the last refresh, unless force is true in which case always clear
	if !force && time.Now().Before(m.lastSessionRefresh.Add(minTimeBetweenRefreshes)) {
		m.logger.Debugw("Skipping session refresh - too soon",
			"lastRefresh", m.lastSessionRefresh,
			"minInterval", minTimeBetweenRefreshes,
			"timeSinceLast", time.Since(m.lastSessionRefresh))
		return
	}

	// clear and release sessions first
	m.clear()

	if err := m.getAndAddSessions(); err != nil {
		m.logger.Warnw("Failed to re-acquire all audio sessions", "error", err)
	} else {
		m.logger.Debugw("Re-acquired sessions successfully",
			"sessionCount", len(m.m))
	}
}

// returns true if a session is not currently mapped to any slider, false otherwise
// special sessions (master, system, mic) and device-specific sessions always count as mapped,
// even when absent from the config. this makes sense for every current feature that uses "unmapped sessions"
func (m *sessionMap) sessionMapped(session Session) bool {

	// count master/system/mic as mapped
	if funk.ContainsString([]string{masterSessionName, systemSessionName, inputSessionName}, session.Key()) {
		return true
	}

	// count device sessions as mapped
	if deviceSessionKeyPattern.MatchString(session.Key()) {
		return true
	}

	matchFound := false

	// look through the actual mappings
	m.deej.config.SliderMapping.iterate(func(sliderIdx int, targets []string) {
		for _, target := range targets {

			// ignore special transforms
			if m.targetHasSpecialTransform(target) {
				continue
			}

			// safe to assume this has a single element because we made sure there's no special transform
			target = m.resolveTarget(target)[0]

			if target == session.Key() {
				matchFound = true
				return
			}
		}
	})

	return matchFound
}

func (m *sessionMap) handleSliderMoveEvent(event SliderMoveEvent) {

	// Get the maximum time between refreshes from config, with fallback to default
	maxTimeBetweenRefreshes := time.Duration(m.deej.config.MaxSessionRefreshInterval) * time.Second
	if maxTimeBetweenRefreshes <= 0 {
		// Use default if not configured
		maxTimeBetweenRefreshes = time.Second * 45
	}

	// first of all, ensure our session map isn't moldy
	if m.lastSessionRefresh.Add(maxTimeBetweenRefreshes).Before(time.Now()) {
		m.logger.Debugw("Stale session map detected on slider move, refreshing",
			"maxInterval", maxTimeBetweenRefreshes,
			"timeSinceLast", time.Since(m.lastSessionRefresh))
		m.refreshSessions(true)
	}

	// For faster response, also refresh if it's been more than 0.5 seconds since last refresh
	// This helps catch new sessions that might have appeared
	if time.Since(m.lastSessionRefresh) > 500*time.Millisecond {
		m.logger.Debugw("Refreshing sessions for faster response on slider move",
			"timeSinceLast", time.Since(m.lastSessionRefresh))
		m.refreshSessions(true)
	}

	// get the targets mapped to this slider from the config
	targets, ok := m.deej.config.SliderMapping.get(event.SliderID)

	// if slider not found in config, silently ignore
	if !ok {
		return
	}

	targetFound := false
	adjustmentFailed := false

	// for each possible target for this slider...
	for _, target := range targets {

		// resolve the target name by cleaning it up and applying any special transformations.
		// depending on the transformation applied, this can result in more than one target name
		resolvedTargets := m.resolveTarget(target)

		// for each resolved target...
		for _, resolvedTarget := range resolvedTargets {

			// check the map for matching sessions
			sessions, ok := m.get(resolvedTarget)

			// no sessions matching this target - move on
			if !ok {
				continue
			}

			targetFound = true

			// iterate all matching sessions and adjust the volume of each one
			for _, session := range sessions {
				if session.GetVolume() != event.PercentValue {
					if err := session.SetVolume(event.PercentValue); err != nil {
						m.logger.Warnw("Failed to set target session volume", "error", err)
						adjustmentFailed = true
					}
				}
			}
		}
	}

	// Hybrid approach: Handle failures with session refresh
	if !targetFound {
		// No target found - refresh sessions to look for new processes
		m.logger.Debug("No target found for slider, refreshing sessions")
		m.refreshSessions(false)
	} else if adjustmentFailed {
		// Volume adjustment failed - force refresh to handle stale sessions
		m.logger.Debug("Volume adjustment failed, forcing session refresh")
		m.refreshSessions(true)
	}
}

func (m *sessionMap) handlePeriodicVolumeUpdate() {
	// Get current slider values from serial component
	currentValues := m.deej.serial.GetCurrentSliderValues()
	if currentValues == nil {
		m.logger.Debug("No current slider values available for periodic update")
		return
	}

	// Get the maximum time between refreshes from config, with fallback to default
	maxTimeBetweenRefreshes := time.Duration(m.deej.config.MaxSessionRefreshInterval) * time.Second
	if maxTimeBetweenRefreshes <= 0 {
		// Use default if not configured
		maxTimeBetweenRefreshes = time.Second * 45
	}

	// first of all, ensure our session map isn't moldy
	if m.lastSessionRefresh.Add(maxTimeBetweenRefreshes).Before(time.Now()) {
		m.logger.Debug("Stale session map detected on periodic update, refreshing")
		m.refreshSessions(true)
	}

	// Hybrid approach: First check for external volume changes
	m.handleExternalVolumeChanges()

	// Track overall success for this periodic update
	overallSuccess := true
	anyAdjustmentsMade := false

	// Iterate through all sliders and update their mapped sessions
	for sliderIdx, percentValue := range currentValues {
		// get the targets mapped to this slider from the config
		targets, ok := m.deej.config.SliderMapping.get(sliderIdx)

		// if slider not found in config, silently ignore
		if !ok {
			continue
		}

		sliderSuccess := true

		// for each possible target for this slider...
		for _, target := range targets {
			// resolve the target name by cleaning it up and applying any special transformations.
			resolvedTargets := m.resolveTarget(target)

			// for each resolved target...
			for _, resolvedTarget := range resolvedTargets {
				// check the map for matching sessions
				sessions, ok := m.get(resolvedTarget)

				// no sessions matching this target - move on
				if !ok {
					continue
				}

				// iterate all matching sessions and adjust the volume of each one
				for _, session := range sessions {
					if session.GetVolume() != percentValue {
						anyAdjustmentsMade = true
						if err := session.SetVolume(percentValue); err != nil {
							m.logger.Warnw("Failed to set target session volume during periodic update",
								"error", err, "slider", sliderIdx, "target", resolvedTarget)
							sliderSuccess = false
							overallSuccess = false
						}
					}
				}
			}
		}

		// If this slider had failures, log it
		if !sliderSuccess {
			m.logger.Debugw("Slider had volume adjustment failures during periodic update", "slider", sliderIdx)
		}
	}

	// Hybrid approach: If we had significant failures, consider refreshing sessions
	if !overallSuccess {
		m.logger.Debug("Periodic update had failures, considering session refresh")
		// Only refresh if we actually made adjustments (to avoid unnecessary refreshes)
		if anyAdjustmentsMade {
			m.refreshSessions(false)
		}
	}

	if m.deej.Verbose() {
		m.logger.Debugw("Completed periodic volume update for all sliders",
			"adjustmentsMade", anyAdjustmentsMade, "overallSuccess", overallSuccess)
	}
}

// handleExternalVolumeChanges detects when sessions have been changed by external applications
// and restores them to the expected slider values
func (m *sessionMap) handleExternalVolumeChanges() {
	// Check if external change detection is enabled
	if !m.deej.config.DetectExternalChanges {
		return
	}

	// Get current slider values
	currentValues := m.deej.serial.GetCurrentSliderValues()
	if currentValues == nil {
		return
	}

	// Check each mapped session to see if its volume differs from expected
	externalChangesDetected := false

	for sliderIdx, expectedValue := range currentValues {
		targets, ok := m.deej.config.SliderMapping.get(sliderIdx)
		if !ok {
			continue
		}

		for _, target := range targets {
			resolvedTargets := m.resolveTarget(target)

			for _, resolvedTarget := range resolvedTargets {
				sessions, ok := m.get(resolvedTarget)
				if !ok {
					continue
				}

				for _, session := range sessions {
					currentVolume := session.GetVolume()
					if util.SignificantlyDifferent(currentVolume, expectedValue, m.deej.config.NoiseReductionLevel) {
						externalChangesDetected = true
						if m.deej.Verbose() {
							m.logger.Debugw("Detected external volume change, restoring",
								"session", resolvedTarget, "current", currentVolume, "expected", expectedValue)
						}

						if err := session.SetVolume(expectedValue); err != nil {
							m.logger.Warnw("Failed to restore volume after external change",
								"error", err, "session", resolvedTarget)
						}
					}
				}
			}
		}
	}

	if externalChangesDetected && m.deej.Verbose() {
		m.logger.Debug("External volume changes detected and restored")
	}
}

func (m *sessionMap) targetHasSpecialTransform(target string) bool {
	return strings.HasPrefix(target, specialTargetTransformPrefix)
}

func (m *sessionMap) resolveTarget(target string) []string {

	// start by ignoring the case
	target = strings.ToLower(target)

	// look for any special targets first, by examining the prefix
	if m.targetHasSpecialTransform(target) {
		return m.applyTargetTransform(strings.TrimPrefix(target, specialTargetTransformPrefix))
	}

	return []string{target}
}

func (m *sessionMap) applyTargetTransform(specialTargetName string) []string {

	// select the transformation based on its name
	switch specialTargetName {

	// get current active window
	case specialTargetCurrentWindow:
		currentWindowProcessNames, err := util.GetCurrentWindowProcessNames()

		// silently ignore errors here, as this is on deej's "hot path" (and it could just mean the user's running linux)
		if err != nil {
			return nil
		}

		// we could have gotten a non-lowercase names from that, so let's ensure we return ones that are lowercase
		for targetIdx, target := range currentWindowProcessNames {
			currentWindowProcessNames[targetIdx] = strings.ToLower(target)
		}

		// remove dupes
		return funk.UniqString(currentWindowProcessNames)

	// get currently unmapped sessions
	case specialTargetAllUnmapped:
		targetKeys := make([]string, len(m.unmappedSessions))
		for sessionIdx, session := range m.unmappedSessions {
			targetKeys[sessionIdx] = session.Key()
		}

		return targetKeys
	}

	return nil
}

func (m *sessionMap) add(value Session) {
	m.lock.Lock()
	defer m.lock.Unlock()

	key := value.Key()

	existing, ok := m.m[key]
	if !ok {
		m.m[key] = []Session{value}
	} else {
		m.m[key] = append(existing, value)
	}
}

func (m *sessionMap) get(key string) ([]Session, bool) {
	m.lock.Lock()
	defer m.lock.Unlock()

	value, ok := m.m[key]
	return value, ok
}

func (m *sessionMap) clear() {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.logger.Debug("Releasing and clearing all audio sessions")

	for key, sessions := range m.m {
		for _, session := range sessions {
			session.Release()
		}

		delete(m.m, key)
	}

	m.logger.Debug("Session map cleared")
}

func (m *sessionMap) String() string {
	m.lock.Lock()
	defer m.lock.Unlock()

	sessionCount := 0

	for _, value := range m.m {
		sessionCount += len(value)
	}

	return fmt.Sprintf("<%d audio sessions>", sessionCount)
}
