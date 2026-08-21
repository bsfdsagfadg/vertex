package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"
	"unicode"

	vertexapi "github.com/bsfdsagfadg/vertex/internal/api"
	"github.com/bsfdsagfadg/vertex/internal/app"
	"github.com/bsfdsagfadg/vertex/internal/buildinfo"
	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/migration"
)

//go:embed rules.txt
//nolint:gochecknoglobals // Embedded file
var rulesText string

const (
	shutdownGrace         = 25 * time.Second
	rulesAgreedName       = ".rules_agreed"
	rulesAgreedDockerName = "agreed-rules-docker.txt"
)

func rulesStatePath(name string) string {
	return filepath.Join(config.ConfigDir(), "state", name)
}

func rulesHash() string {
	cleanText := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, rulesText)
	sum := sha256.Sum256([]byte(cleanText))
	return hex.EncodeToString(sum[:])[:16]
}

func inDocker() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		s := string(data)
		if strings.Contains(s, "docker") || strings.Contains(s, "containerd") || strings.Contains(s, "kubepods") {
			return true
		}
	}
	return false
}

// 提取原有的终端普通打印，仅在需要同意规则阶段展示
func printLegacyBanner(info buildinfo.BuildInfo) {
	fmt.Println("╔══════════════════════════════════════════════════════════════╗")
	fmt.Printf("║  Vertex AI Proxy  %-42s ║\n", info.Version)
	fmt.Println("║  PolyForm Noncommercial License 1.0.0   Deconstructed_Cube   ║")
	fmt.Printf("║  Build: %s / %s                                  ║\n", info.Commit, info.BuildTime)
	fmt.Printf("║  Platform: %s/%s                                       ║\n", runtime.GOOS, runtime.GOARCH)
	fmt.Println("╚══════════════════════════════════════════════════════════════╝")

	fmt.Println()
	fmt.Println("  ╔══════════════════════════════════════════════════════════╗")
	fmt.Println("  ║                                                          ║")
	fmt.Println("  ║   ⚠️  本软件完全免费，如果你花钱购买了这个软件，         ║")
	fmt.Println("  ║       你被骗了。请立即联系卖家退款。                     ║")
	fmt.Println("  ║                                                          ║")
	fmt.Println("  ║   获取正版：https://discord.gg/odysseia                  ║")
	fmt.Println("  ║                                                          ║")
	fmt.Println("  ╚══════════════════════════════════════════════════════════╝")
	fmt.Println()
}

func main() {
	build := buildinfo.Current()
	setupTermuxCerts() // 优先初始化 Termux 证书
	migrationService, err := migration.NewService(config.ConfigDir())
	if err != nil {
		log.Fatalf("[vproxy] failed to initialize migration service: %v", err)
	}
	migrationStatus, err := migrationService.InspectAndMark(context.Background())
	if err != nil {
		log.Fatalf("[vproxy] failed to inspect persistent layout: %v", err)
	}
	if migrationStatus.Required {
		// Migration uses the same administrator password as the normal console;
		// initialize it before starting the isolated migration server instead of
		// creating a separate one-time token.
		vertexapi.EnsureAdminPasswordWithProvider(config.GetProvider())
		migrationApp, appErr := app.NewMigration(app.MigrationOptions{
			Build: build, Service: migrationService, Status: migrationStatus, ShutdownGrace: shutdownGrace,
		})
		if appErr != nil {
			log.Fatalf("[Migration] 无法构建迁移模式: %v", appErr)
		}
		rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		appErr = migrationApp.Run(rootCtx)
		stop()
		if appErr != nil && !errors.Is(appErr, app.ErrRestartNormal) {
			log.Fatalf("[Migration] 迁移服务退出异常: %v", appErr)
		}
		if !errors.Is(appErr, app.ErrRestartNormal) {
			return
		}
	}

	// ---- 状态文件迁移（提前执行，无输出） ----
	migrateStateFile(filepath.Join(config.ConfigDir(), rulesAgreedName), rulesStatePath(rulesAgreedName))
	migrateStateFile(filepath.Join(config.ConfigDir(), rulesAgreedDockerName), rulesStatePath(rulesAgreedDockerName))

	// ---- 规则同意检查 ----
	curHash := rulesHash()
	if inDocker() {
		if !checkRulesAgreedDocker(curHash) {
			printLegacyBanner(build)
			fmt.Println()
			fmt.Println("  ╔══════════════════════════════════════════════════════════╗")
			fmt.Println("  ║  📦 检测到 Docker 环境                                   ║")
			fmt.Println("  ╚══════════════════════════════════════════════════════════╝")
			fmt.Println()
			fmt.Println("  Docker 容器中无法交互同意规则。请按以下步骤同意：")
			fmt.Println()
			fmt.Println("  1) 在挂载到容器 /app/config 的本机目录中创建文件：")
			fmt.Println("       config/state/agreed-rules-docker.txt")
			fmt.Println()
			fmt.Println("  2) 文件内容写入当前规则版本哈希（必须完全匹配）：")
			fmt.Printf("       %s\n", curHash)
			fmt.Println()
			fmt.Println("     一行命令：")
			fmt.Printf("       echo %s > ./config/state/agreed-rules-docker.txt\n", curHash)
			fmt.Println()
			fmt.Println("  3) 重启容器即可。")
			fmt.Println()
			os.Exit(0)
		}
	} else {
		if !checkRulesAgreed(curHash) {
			printLegacyBanner(build)
			fmt.Println(rulesText)
			fmt.Println()
			if hasOldAgreement() {
				fmt.Println("  ⚠️  规则已更新，需要您重新确认。")
				fmt.Println()
			}
			fmt.Print("  请输入 yes 同意以上规则（输入其他内容退出）：")
			reader := bufio.NewReader(os.Stdin)
			input, _ := reader.ReadString('\n')
			input = strings.TrimSpace(strings.ToLower(input))
			if input != "yes" {
				fmt.Println("  你未同意规则，程序退出。")
				os.Exit(0)
			}
			saveRulesAgreed(curHash)
			fmt.Println()
			fmt.Println("  ✓ 已同意规则，正在启动...")
			fmt.Println()
		}
	}

	normalApp, err := app.NewNormal(app.NormalOptions{
		Build: build, ConfigDir: config.ConfigDir(), ShutdownGrace: shutdownGrace,
	})
	if err != nil {
		log.Fatalf("[vproxy] failed to construct application: %v", err)
	}
	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := normalApp.Run(rootCtx); err != nil {
		log.Fatalf("[vproxy] application stopped with error: %v", err)
	}
	log.Printf("[vproxy] 关闭完成，程序退出")
}

func checkRulesAgreed(curHash string) bool {
	data, err := os.ReadFile(rulesStatePath(rulesAgreedName))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), curHash)
}

func hasOldAgreement() bool {
	data, err := os.ReadFile(rulesStatePath(rulesAgreedName))
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(data))) > 0
}

func saveRulesAgreed(curHash string) {
	_ = os.MkdirAll(filepath.Join(config.ConfigDir(), "state"), 0o700)
	line := fmt.Sprintf("%s\t%s\n", time.Now().Format(time.RFC3339), curHash)
	_ = os.WriteFile(rulesStatePath(rulesAgreedName), []byte(line), 0o600)
}

func checkRulesAgreedDocker(curHash string) bool {
	data, err := os.ReadFile(rulesStatePath(rulesAgreedDockerName))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), curHash)
}

func migrateStateFile(oldPath, newPath string) {
	if _, err := os.Stat(oldPath); err != nil {
		return
	}
	if _, err := os.Stat(newPath); err == nil {
		_ = os.Remove(oldPath)
		return
	}
	if err := os.MkdirAll(filepath.Dir(newPath), 0o700); err != nil {
		return
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		if data, err := os.ReadFile(oldPath); err == nil {
			if err := os.WriteFile(newPath, data, 0o600); err == nil {
				_ = os.Remove(oldPath)
			}
		}
	}
}
