package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	ccProtocolVersion = "1.53.1"
	ccFPSalt          = "command-code:device-fingerprint:v1"
	ccDevicePlatform  = "win32"
	ccDeviceArch      = "x64"
	ccDeviceOSRelease = "10.0.22631"

	ccInitRefreshDuration = 8 * time.Hour
	ccInitJitterDuration  = 2 * time.Hour
	ccSessionDuration     = 12 * time.Hour
	ccKeyStateMaxEntries  = 4096
)

type ccCPUEntry struct {
	Model string
	Cores int
}

var ccFingerprintCPUs = []ccCPUEntry{
	{Model: "12th Gen Intel(R) Core(TM) i7-12650H", Cores: 10},
	{Model: "12th Gen Intel(R) Core(TM) i5-12400F", Cores: 6},
	{Model: "12th Gen Intel(R) Core(TM) i9-12900K", Cores: 16},
	{Model: "13th Gen Intel(R) Core(TM) i7-13700K", Cores: 16},
	{Model: "13th Gen Intel(R) Core(TM) i5-13600K", Cores: 14},
	{Model: "13th Gen Intel(R) Core(TM) i9-13900K", Cores: 24},
	{Model: "Intel(R) Core(TM) Ultra 7 155H", Cores: 16},
	{Model: "Intel(R) Core(TM) Ultra 9 285H", Cores: 16},
	{Model: "Intel(R) Core(TM) i9-14900K", Cores: 24},
	{Model: "Intel(R) Core(TM) i7-14700K", Cores: 20},
	{Model: "AMD Ryzen 7 7800X3D", Cores: 8},
	{Model: "AMD Ryzen 9 7950X", Cores: 16},
	{Model: "AMD Ryzen 5 7600", Cores: 6},
	{Model: "AMD Ryzen 9 7900X", Cores: 12},
	{Model: "AMD Ryzen 7 5800X3D", Cores: 8},
}

var ccFingerprintMems = []int{8, 16, 24, 32, 48, 64}

var ccFingerprintTZs = []string{
	"America/New_York", "America/Chicago", "America/Los_Angeles", "America/Toronto",
	"Europe/London", "Europe/Berlin", "Europe/Paris", "Europe/Moscow",
	"Asia/Shanghai", "Asia/Tokyo", "Asia/Singapore", "Asia/Seoul", "Asia/Hong_Kong",
	"Australia/Sydney", "Pacific/Auckland",
}

var ccFingerprintMacCounts = []int{2, 3, 4, 5}
var ccFingerprintOSUsers = []string{"dev", "user", "admin", "coder", "engineer", "work"}
var ccFingerprintMailDomains = []string{"gmail.com", "outlook.com", "qq.com", "163.com"}

type ccDeviceFingerprint struct {
	Thumbmark  string                 `json:"thumbmark"`
	Components map[string]interface{} `json:"components"`
}

func ccSha256(data string) []byte {
	h := sha256.Sum256([]byte(data))
	return h[:]
}

func ccFpDigest(apiKey, field, salt string) []byte {
	return ccSha256(fmt.Sprintf("%s\x00%s\x00%s", salt, apiKey, field))
}

func ccFpPickIndex(apiKey, field string, count int, labelOf func(int) string, salt string) int {
	bestIdx := 0
	var bestScore []byte
	for i := 0; i < count; i++ {
		score := ccFpDigest(apiKey, fmt.Sprintf("%s\x00%s", field, labelOf(i)), salt)
		if bestScore == nil || bytes.Compare(score, bestScore) > 0 {
			bestScore = score
			bestIdx = i
		}
	}
	return bestIdx
}

func ccFingerprintHash(value string) string {
	v := strings.ToLower(strings.TrimSpace(value))
	if v == "" {
		return ""
	}
	digest := ccSha256(fmt.Sprintf("%s\x00%s", ccFPSalt, v))
	return hex.EncodeToString(digest)
}

func ccGenerateFingerprint(apiKey, salt string) ccDeviceFingerprint {
	cpuIdx := ccFpPickIndex(apiKey, "cpu", len(ccFingerprintCPUs), func(i int) string {
		return fmt.Sprintf("%s|%d", ccFingerprintCPUs[i].Model, ccFingerprintCPUs[i].Cores)
	}, salt)
	cpuEntry := ccFingerprintCPUs[cpuIdx]

	memIdx := ccFpPickIndex(apiKey, "mem", len(ccFingerprintMems), func(i int) string {
		return fmt.Sprintf("%d", ccFingerprintMems[i])
	}, salt)
	memGiB := ccFingerprintMems[memIdx]

	tzIdx := ccFpPickIndex(apiKey, "timezone", len(ccFingerprintTZs), func(i int) string {
		return ccFingerprintTZs[i]
	}, salt)
	tz := ccFingerprintTZs[tzIdx]

	macCountIdx := ccFpPickIndex(apiKey, "macCount", len(ccFingerprintMacCounts), func(i int) string {
		return fmt.Sprintf("%d", ccFingerprintMacCounts[i])
	}, salt)
	macCount := ccFingerprintMacCounts[macCountIdx]

	osUserIdx := ccFpPickIndex(apiKey, "osUser", len(ccFingerprintOSUsers), func(i int) string {
		return ccFingerprintOSUsers[i]
	}, salt)
	osUser := ccFingerprintOSUsers[osUserIdx]

	mailDomainIdx := ccFpPickIndex(apiKey, "mailDomain", len(ccFingerprintMailDomains), func(i int) string {
		return ccFingerprintMailDomains[i]
	}, salt)
	mailDomain := ccFingerprintMailDomains[mailDomainIdx]

	hexDigest := func(field string, byteCount int) string {
		d := ccFpDigest(apiKey, field, salt)
		if byteCount > len(d) {
			byteCount = len(d)
		}
		return hex.EncodeToString(d[:byteCount])
	}

	mid := hexDigest("machineId", 16)
	machineID := fmt.Sprintf("%s-%s-%s-%s-%s", mid[:8], mid[8:12], mid[12:16], mid[16:20], mid[20:32])

	macs := make([]string, 0, macCount)
	for i := 0; i < macCount; i++ {
		b := ccFpDigest(apiKey, fmt.Sprintf("mac%d", i), salt)
		mac := fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", b[0], b[1], b[2], b[3], b[4], b[5])
		macs = append(macs, mac)
	}
	sort.Strings(macs)

	hostname := "DESKTOP-" + strings.ToUpper(hexDigest("hostname", 4))
	gitEmail := fmt.Sprintf("%s.%s@%s", osUser, hexDigest("gitEmail", 3), mailDomain)

	machineIDHash := ccFingerprintHash(machineID)
	macHashes := make([]string, 0, len(macs))
	for _, m := range macs {
		if h := ccFingerprintHash(m); h != "" {
			macHashes = append(macHashes, h)
		}
	}
	osUserHash := ccFingerprintHash(osUser)
	hostnameHash := ccFingerprintHash(hostname)
	gitEmailHash := ccFingerprintHash(gitEmail)

	thumbSeed := []string{strings.TrimSpace(machineID), strings.Join(macs, ",")}
	if strings.TrimSpace(machineID) == "" {
		thumbSeed = append(thumbSeed, hostname, cpuEntry.Model)
	}
	thumbmark := hex.EncodeToString(ccSha256(fmt.Sprintf("%s\x00machine\x00%s", ccFPSalt, strings.Join(thumbSeed, "|"))))

	components := map[string]interface{}{
		"machineIdHash":    machineIDHash,
		"macHashes":        macHashes,
		"osUserHash":       osUserHash,
		"hostnameHash":     hostnameHash,
		"gitEmailHash":     gitEmailHash,
		"platform":         ccDevicePlatform,
		"arch":             ccDeviceArch,
		"osRelease":        ccDeviceOSRelease,
		"cpuModel":         cpuEntry.Model,
		"cpuCount":         cpuEntry.Cores,
		"memGiB":           memGiB,
		"isContainer":      false,
		"timezone":         tz,
		"runtime":          "cli",
		"collectorVersion": 1,
	}

	return ccDeviceFingerprint{
		Thumbmark:  thumbmark,
		Components: components,
	}
}

func ccSessionForKey(apiKey string, now time.Time) string {
	bucket := now.Unix() / int64(ccSessionDuration.Seconds())
	digest := ccSha256(fmt.Sprintf("cc-session\x00%s\x00%d", apiKey, bucket))
	h := hex.EncodeToString(digest[:16])
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[:8], h[8:12], h[12:16], h[16:20], h[20:32])
}

// Global in-memory cache for initialization status per site credential.
var (
	ccKeyStateMu    sync.Mutex
	ccKeyStateStore = map[string]ccKeyState{}
)

type ccKeyState struct {
	nextInitAt time.Time
	touchedAt  time.Time
}

func ensureCommandCodeInitialized(ctx context.Context, client *http.Client, baseURL, apiKey, salt string) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return
	}
	identity := strings.TrimSpace(salt)
	if identity == "" {
		identity = ccFingerprintHash(apiKey)
	}
	storeKey := fmt.Sprintf("%s:%s", identity, strings.TrimRight(strings.TrimSpace(baseURL), "/"))

	ccKeyStateMu.Lock()
	now := time.Now()
	state, exists := ccKeyStateStore[storeKey]
	if exists && now.Before(state.nextInitAt) {
		state.touchedAt = now
		ccKeyStateStore[storeKey] = state
		ccKeyStateMu.Unlock()
		return
	}
	if _, exists := ccKeyStateStore[storeKey]; !exists && len(ccKeyStateStore) >= ccKeyStateMaxEntries {
		var oldestKey string
		var oldestTouched time.Time
		for key, item := range ccKeyStateStore {
			if oldestTouched.IsZero() || item.touchedAt.Before(oldestTouched) {
				oldestKey = key
				oldestTouched = item.touchedAt
			}
		}
		if oldestKey != "" {
			delete(ccKeyStateStore, oldestKey)
		}
	}
	// Schedule next initialization with 8h + 2h jitter
	j := ccFpDigest(apiKey, "init-jitter", salt)
	jitterMs := int64((uint32(j[0])<<24 | uint32(j[1])<<16 | uint32(j[2])<<8 | uint32(j[3])) % uint32(ccInitJitterDuration.Milliseconds()))
	nextInitAt := now.Add(ccInitRefreshDuration + time.Duration(jitterMs)*time.Millisecond)
	ccKeyStateStore[storeKey] = ccKeyState{
		nextInitAt: nextInitAt,
		touchedAt:  now,
	}
	ccKeyStateMu.Unlock()

	// Fire-and-forget in goroutine with detached context
	go func() {
		initialized := false
		defer func() {
			ccKeyStateMu.Lock()
			state, stillCurrent := ccKeyStateStore[storeKey]
			if !stillCurrent || !state.nextInitAt.Equal(nextInitAt) {
				ccKeyStateMu.Unlock()
				return
			}
			if !initialized {
				delete(ccKeyStateStore, storeKey)
			}
			ccKeyStateMu.Unlock()
		}()
		reqCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if client == nil {
			client = http.DefaultClient
		}

		fp := ccGenerateFingerprint(apiKey, salt)
		fpBody, _ := json.Marshal(fp)

		// 1. Post /alpha/fingerprint/record
		recordReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/alpha/fingerprint/record", bytes.NewReader(fpBody))
		recordOK := false
		if err == nil {
			recordReq.Header.Set("Authorization", "Bearer "+apiKey)
			recordReq.Header.Set("Content-Type", "application/json")
			recordReq.Header.Set("User-Agent", "cli")
			recordReq.Header.Set("x-command-code-version", ccProtocolVersion)
			recordReq.Header.Set("x-cli-environment", "production")
			if resp, err := client.Do(recordReq); err == nil {
				recordOK = resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices
				_ = resp.Body.Close()
			}
		}

		// 2. Post /alpha/lifecycle-events
		randBytes := make([]byte, 4)
		_, _ = rand.Read(randBytes)
		sessID := fmt.Sprintf("sess_%x", randBytes)

		lifecycleBody, _ := json.Marshal(map[string]interface{}{
			"eventType": "cli_session_exists",
			"metadata": map[string]interface{}{
				"sessionId":  sessID,
				"cliVersion": ccProtocolVersion,
				"mode":       "interactive",
				"os":         fmt.Sprintf("%s-%s", ccDevicePlatform, ccDeviceArch),
			},
		})

		lifeReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/alpha/lifecycle-events", bytes.NewReader(lifecycleBody))
		if err == nil {
			lifeReq.Header.Set("Authorization", "Bearer "+apiKey)
			lifeReq.Header.Set("Content-Type", "application/json")
			lifeReq.Header.Set("User-Agent", "cli")
			lifeReq.Header.Set("x-command-code-version", ccProtocolVersion)
			lifeReq.Header.Set("x-cli-environment", "production")
			if resp, err := client.Do(lifeReq); err == nil {
				initialized = recordOK && resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices
				_ = resp.Body.Close()
			}
		}
	}()
}
