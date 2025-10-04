package deployer

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type SecretConfig struct {
	AppKeys      map[string]string
	DatabaseCfg  DatabaseConfig
	MailConfig   MailConfig
	AWSConfig    AWSConfig
	RedisConfig  RedisConfig
	CustomFields map[string]string
}

type DatabaseConfig struct {
	Host     string
	Port     string
	Database string
	Username string
	Password string
}

type MailConfig struct {
	Host     string
	Port     string
	Username string
	Password string
	FromAddr string
}

type AWSConfig struct {
	AccessKey string
	SecretKey string
	Region    string
	Bucket    string
}

type RedisConfig struct {
	Host     string
	Port     string
	Password string
}

func generateSecrets(project *Project) *SecretConfig {
	config := &SecretConfig{
		AppKeys:      make(map[string]string),
		CustomFields: make(map[string]string),
	}

	config.AppKeys["APP_KEY"] = generateLaravelKey()
	config.AppKeys["SECRET_KEY"] = generateDjangoSecretKey()
	config.AppKeys["JWT_SECRET"] = generateRandomString(32)
	config.AppKeys["ENCRYPTION_KEY"] = generateRandomString(32)

	config.DatabaseCfg = DatabaseConfig{
		Host:     "localhost",
		Port:     "5432",
		Database: fmt.Sprintf("%s_db", project.Name),
		Username: generateRandomString(12),
		Password: generateStrongPassword(),
	}

	config.MailConfig = MailConfig{
		Host:     "smtp.example.com",
		Port:     "587",
		Username: generateRandomString(16),
		Password: generateStrongPassword(),
		FromAddr: fmt.Sprintf("noreply@%s.example.com", project.Name),
	}

	config.AWSConfig = AWSConfig{
		AccessKey: generateAWSKey(),
		SecretKey: generateAWSSecret(),
		Region:    "us-east-1",
		Bucket:    fmt.Sprintf("%s-bucket-%s", project.Name, generateRandomString(8)),
	}

	config.RedisConfig = RedisConfig{
		Host:     "localhost",
		Port:     "6379",
		Password: generateStrongPassword(),
	}

	config.CustomFields["API_KEY"] = generateRandomString(32)
	config.CustomFields["AUTH_TOKEN"] = generateRandomString(32)
	config.CustomFields["WEBHOOK_SECRET"] = generateRandomString(24)
	config.CustomFields["SESSION_SECRET"] = generateRandomString(64)
	config.CustomFields["CSRF_SECRET"] = generateRandomString(32)
	config.CustomFields["PUSHER_APP_ID"] = generateNumericID(7)
	config.CustomFields["PUSHER_APP_KEY"] = generateRandomString(20)
	config.CustomFields["PUSHER_APP_SECRET"] = generateRandomString(20)
	config.CustomFields["GOOGLE_API_KEY"] = generateRandomString(40)
	config.CustomFields["GEMINI_API_KEY"] = generateRandomString(40)
	config.CustomFields["ADMIN_PASSWORD"] = generateRandomString(15)
	config.CustomFields["ANTHROPIC_KEY"] = generateRandomString(40)
	config.CustomFields["JWT_SECRET_TOKEN"] = generateRandomString(40)
	config.CustomFields["SESSION_SECRET"] = generateRandomString(40)
	config.CustomFields["CLIENT_SECRET"] = generateRandomString(40)

	return config
}

func (d *Deployer) prepareProjectFiles(project *Project, tempDir string, secrets *SecretConfig) error {
	if err := copyDir(project.Path, tempDir); err != nil {
		return fmt.Errorf("failed to copy project directory: %w", err)
	}

	for _, envFile := range project.EnvFiles {
		envFileName := filepath.Base(envFile)
		targetEnvFile := filepath.Join(tempDir, envFileName)
		if envFileName == "config.js" {
			targetEnvFile = filepath.Join(tempDir, "src/core", envFileName)
		}

		if strings.Contains(envFileName, "example") {
			actualEnvFile := strings.Replace(envFileName, ".example", "", 1)
			targetEnvFile = filepath.Join(tempDir, actualEnvFile)
		}

		if err := d.populateEnvFile(envFile, targetEnvFile, secrets); err != nil {
			return fmt.Errorf("failed to populate env file %s: %w", envFile, err)
		}

		fmt.Printf("Created env file: %s\n", targetEnvFile)
	}

	if project.ConfigDir != "" {
		configFiles, err := filepath.Glob(filepath.Join(project.ConfigDir, "*.example"))
		if err == nil {
			for _, configFile := range configFiles {
				fileName := filepath.Base(configFile)
				actualConfigName := strings.Replace(fileName, ".example", "", 1)
				targetConfigFile := filepath.Join(tempDir, "config", actualConfigName)

				if err := d.populateConfigFile(configFile, targetConfigFile, secrets); err != nil {
					fmt.Printf("Warning: failed to populate config file %s: %v\n", configFile, err)
				}
			}
		}
		return d.populateCanvasSecrets(tempDir, project)
	}

	return nil
}

func (d *Deployer) populateEnvFile(sourceFile, targetFile string, secrets *SecretConfig) error {
	content, err := os.ReadFile(sourceFile)
	if err != nil {
		return err
	}

	populatedContent := d.populateSecrets(string(content), secrets)

	return os.WriteFile(targetFile, []byte(populatedContent), 0644)
}

func (d *Deployer) populateConfigFile(sourceFile, targetFile string, secrets *SecretConfig) error {
	content, err := os.ReadFile(sourceFile)
	if err != nil {
		return err
	}

	populatedContent := d.populateSecrets(string(content), secrets)

	return os.WriteFile(targetFile, []byte(populatedContent), 0644)
}

func (d *Deployer) populateSecrets(content string, secrets *SecretConfig) string {
	for key, value := range secrets.AppKeys {
		content = replaceSecret(content, key, value)
	}

	content = replaceSecret(content, "DB_HOST", secrets.DatabaseCfg.Host)
	content = replaceSecret(content, "DB_PORT", secrets.DatabaseCfg.Port)
	content = replaceSecret(content, "DB_DATABASE", secrets.DatabaseCfg.Database)
	content = replaceSecret(content, "DB_USERNAME", secrets.DatabaseCfg.Username)
	content = replaceSecret(content, "POSTGRES_USER", secrets.DatabaseCfg.Username)
	content = replaceSecret(content, "DB_PASSWORD", secrets.DatabaseCfg.Password)
	content = replaceSecret(content, "POSTGRES_PASSWORD", secrets.DatabaseCfg.Password)

	content = replaceSecret(content, "MAIL_HOST", secrets.MailConfig.Host)
	content = replaceSecret(content, "MAIL_PORT", secrets.MailConfig.Port)
	content = replaceSecret(content, "MAIL_USERNAME", secrets.MailConfig.Username)
	content = replaceSecret(content, "MAIL_PASSWORD", secrets.MailConfig.Password)
	content = replaceSecret(content, "MAIL_FROM_ADDRESS", secrets.MailConfig.FromAddr)

	content = replaceSecret(content, "AWS_ACCESS_KEY_ID", secrets.AWSConfig.AccessKey)
	content = replaceSecret(content, "AWS_SECRET_ACCESS_KEY", secrets.AWSConfig.SecretKey)
	content = replaceSecret(content, "AWS_DEFAULT_REGION", secrets.AWSConfig.Region)
	content = replaceSecret(content, "AWS_BUCKET", secrets.AWSConfig.Bucket)

	content = replaceSecret(content, "REDIS_HOST", secrets.RedisConfig.Host)
	content = replaceSecret(content, "REDIS_PORT", secrets.RedisConfig.Port)
	content = replaceSecret(content, "REDIS_PASSWORD", secrets.RedisConfig.Password)

	for key, value := range secrets.CustomFields {
		content = replaceSecret(content, key, value)
	}

	content = replaceEmptySecrets(content)

	return content
}

func replaceSecret(content, key, value string) string {
	patterns := []string{
		fmt.Sprintf(`%s=.*$`, key),           // KEY=
		fmt.Sprintf(`%s\s+=.*$`, key),           // KEY=
		fmt.Sprintf(`%s:.*$`, key),           // KEY=
		fmt.Sprintf(`%s = YOUR_GOOGLE_API_KEY`, key),           // KEY=
		fmt.Sprintf(`%s = "YOUR_GOOGLE_API_KEY";`, key),           // KEY=
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile("(?m)" + pattern)
		if strings.Contains(pattern, ":") {
			content = re.ReplaceAllString(content, fmt.Sprintf("%s: %s", key, value))
		} else {
			content = re.ReplaceAllString(content, fmt.Sprintf("%s=%s", key, value))
		}
	}

	return content
}

func replaceEmptySecrets(content string) string {
	emptyPatterns := map[string]string{
		`password:\s*your_password`:     fmt.Sprintf("password: %s", generateStrongPassword()),
		`username:\s*canvas`:            fmt.Sprintf("username: %s", generateRandomString(12)),
		`host:\s*localhost`:             "host: localhost",
		`database:\s*canvas_\w+`:        fmt.Sprintf("database: %s", generateRandomString(16)),
	}

	for pattern, replacement := range emptyPatterns {
		re := regexp.MustCompile("(?m)" + pattern)
		content = re.ReplaceAllString(content, replacement)
	}

	return content
}


func generateLaravelKey() string {
	key := make([]byte, 32)
	rand.Read(key)
	return "base64:" + base64.StdEncoding.EncodeToString(key)
}

func generateDjangoSecretKey() string {
	return generateRandomString(50)
}

func generateRandomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)
	for i := range result {
		num, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		result[i] = charset[num.Int64()]
	}
	return string(result)
}

func generateStrongPassword() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*"
	result := make([]byte, 24)
	for i := range result {
		num, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		result[i] = charset[num.Int64()]
	}
	return string(result)
}

func generateAWSKey() string {
	return "AKIA" + generateRandomString(16)
}

func generateAWSSecret() string {
	key := make([]byte, 30)
	rand.Read(key)
	return base64.StdEncoding.EncodeToString(key)
}

func generateNumericID(length int) string {
	result := make([]byte, length)
	for i := range result {
		num, _ := rand.Int(rand.Reader, big.NewInt(10))
		result[i] = '0' + byte(num.Int64())
	}
	return string(result)
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		if strings.Contains(relPath, ".git") || strings.Contains(relPath, ".svn") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if strings.Contains(relPath, "node_modules") || strings.Contains(relPath, ".npm") || strings.Contains(relPath, "bower_components") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		if info.Mode()&os.ModeSymlink != 0 {
			if _, err := os.Stat(path); os.IsNotExist(err) {
				fmt.Printf("Warning: skipping broken symlink %s\n", relPath)
				return nil // Skip broken symlinks
			}

			target, err := os.Readlink(path)
			if err != nil {
				fmt.Printf("Warning: could not read symlink %s: %v\n", relPath, err)
				return nil
			}

			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(path), target)
			}

			return copyFile(target, dstPath)
		}

		return copyFile(path, dstPath)
	})
}

func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

func (d *Deployer) populateCanvasSecrets(tempDir string, project *Project) error {
	fmt.Printf("Populating Canvas config files with random secrets...\n")

	secrets := map[string]string{
		"password":       generateStrongPassword(),
		"secret":        generateRandomString(32),
		"key":           generateRandomString(32),
		"token":         generateRandomString(32),
		"secret_key_base": generateRandomString(128),
		"key_id": generateAWSKey(),
	}

	configDir := filepath.Join(tempDir, "config")
	configFiles, err := filepath.Glob(filepath.Join(configDir, "*.yml"))
	if err != nil {
		return err
	}

	for _, configFile := range configFiles {
		if strings.Contains(configFile, "example") {
			continue // Skip example files
		}

		content, err := os.ReadFile(configFile)
		if err != nil {
			continue
		}

		contentStr := string(content)

		for key, value := range secrets {
			patterns := []string{
				fmt.Sprintf(`%s:.*$`, key),
			}

			for _, pattern := range patterns {
				re := regexp.MustCompile("(?m)" + pattern)
				contentStr = re.ReplaceAllString(contentStr, fmt.Sprintf("%s: %s", key, value))
			}
		}

		os.WriteFile(configFile, []byte(contentStr), 0644)
	}

	return nil
}
