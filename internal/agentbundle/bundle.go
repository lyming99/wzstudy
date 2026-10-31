package agentbundle

import (
	"archive/zip"
	"bytes"
	"embed"
	"errors"
	"fmt"
	"strings"
)

// The released server embeds the Windows agent binary so administrators can
// download it or generate a ready-to-run deployment package without uploading
// anything. Development builds only contain the templates and report
// ErrNotBundled.
//
//go:embed all:assets
var assets embed.FS

var ErrNotBundled = errors.New("当前服务端构建未内置采集端程序，请使用包含 agent 的完整发布包")

const binaryPath = "assets/wzstudy-agent.exe"

func Available() bool {
	_, err := assets.ReadFile(binaryPath)
	return err == nil
}

func Binary() ([]byte, error) {
	data, err := assets.ReadFile(binaryPath)
	if err != nil || len(data) == 0 {
		return nil, ErrNotBundled
	}
	return data, nil
}

// ModelConfig is written into the generated agent.env so the downloaded package
// can start without editing files. The API key only exists in that archive.
type ModelConfig struct {
	BaseURL string
	APIKey  string
	Model   string
}

func DefaultModelConfig() ModelConfig {
	return ModelConfig{BaseURL: "https://api.deepseek.com/v1", Model: "deepseek-chat"}
}

func Build(serverURL, agentKey, name string, model ModelConfig) ([]byte, string, error) {
	binary, err := Binary()
	if err != nil {
		return nil, "", err
	}
	return BuildWith(binary, serverURL, agentKey, name, model)
}

// BuildWith assembles a deployment archive: the agent executable, a generated
// agent.env carrying the one-time key and model settings, launch scripts, skills
// and a readme.
func BuildWith(binary []byte, serverURL, agentKey, name string, model ModelConfig) ([]byte, string, error) {
	if len(binary) == 0 {
		return nil, "", ErrNotBundled
	}
	if !strings.HasPrefix(agentKey, "wzag_") {
		return nil, "", errors.New("采集端密钥无效")
	}
	serverURL = strings.TrimRight(strings.TrimSpace(serverURL), "/")
	if serverURL == "" {
		return nil, "", errors.New("服务端地址不能为空")
	}
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	add := func(path string, data []byte) error {
		writer, err := archive.Create(path)
		if err != nil {
			return err
		}
		_, err = writer.Write(data)
		return err
	}
	if err := add("wzstudy-agent.exe", binary); err != nil {
		return nil, "", err
	}
	if err := add("agent.env", []byte(agentEnvironment(serverURL, agentKey, model))); err != nil {
		return nil, "", err
	}
	// Keep the default runtime layout: program files sit next to work/.
	if _, err := archive.Create("work/"); err != nil {
		return nil, "", err
	}
	for _, path := range []string{
		"start-agent.cmd",
		"start-agent.ps1",
		"AGENT_README.md",
		"skills/web-article/SKILL.md",
	} {
		data, err := assets.ReadFile("assets/" + path)
		if err != nil {
			return nil, "", fmt.Errorf("缺少部署模板 %s: %w", path, err)
		}
		if err := add(path, data); err != nil {
			return nil, "", err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, "", err
	}
	return buffer.Bytes(), "wzstudy-agent-" + safeName(name) + ".zip", nil
}

func agentEnvironment(serverURL, agentKey string, model ModelConfig) string {
	baseURL := strings.TrimSpace(model.BaseURL)
	modelName := strings.TrimSpace(model.Model)
	if baseURL == "" {
		baseURL = DefaultModelConfig().BaseURL
	}
	if modelName == "" {
		modelName = DefaultModelConfig().Model
	}
	apiKey := strings.TrimSpace(model.APIKey)
	return fmt.Sprintf(`# 由 WzStudy 一键部署生成，请不要把本文件提交到版本库或发给他人。
# 模型配置已预填；如需更换模型，修改下面的值后重新运行 start-agent.cmd。
WZSTUDY_SERVER_URL=%s
WZSTUDY_AGENT_KEY=%s
AGENT_MODEL_BASE_URL=%s
AGENT_MODEL_API_KEY=%s
AGENT_MODEL=%s
AGENT_SKILLS_DIR=skills
`, serverURL, agentKey, baseURL, apiKey, modelName)
}

func safeName(name string) string {
	name = strings.TrimSpace(name)
	var builder strings.Builder
	for _, character := range name {
		switch {
		case character >= 'a' && character <= 'z',
			character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9',
			character == '-', character == '_',
			character >= 0x4E00 && character <= 0x9FFF:
			builder.WriteRune(character)
		default:
			builder.WriteRune('-')
		}
		if builder.Len() >= 40 {
			break
		}
	}
	cleaned := strings.Trim(builder.String(), "-_")
	if cleaned == "" {
		return "agent"
	}
	return cleaned
}
