package tool

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent/internal/multimodal"
	"github.com/yurika0211/luckyagent/internal/utils"
)

const (
	maxImageAnalyzeImageBytes = 20 << 20
	maxImageAnalyzeDocBytes   = 50 << 20
)

// ImageGenerationDefaults captures configurable defaults for the image_generate tool.
type ImageGenerationDefaults struct {
	Model             string
	Size              string
	Quality           string
	Background        string
	OutputFormat      string
	OutputCompression int
	Count             int
}

// TTSDefaults captures configurable defaults for the text_to_speech tool.
type TTSDefaults struct {
	Model  string
	Voice  string
	Format string
	Speed  float64
}

// ImageAnalyzeTool analyzes images, screenshots, and simple documents through the multimodal processor.
func ImageAnalyzeTool(processor *multimodal.Processor, defaultProvider string) *Tool {
	return &Tool{
		Name:         "image_analyze",
		Description:  "Analyze an image, screenshot, chart, or scanned document. Extract visible text, summarize UI or visual content, and surface likely errors or key signals.",
		Category:     CatBuiltin,
		Source:       "builtin",
		Permission:   PermAuto,
		ParallelSafe: true,
		Parameters: map[string]Param{
			"path": {
				Type:        "string",
				Description: "Local file path to the image or document.",
				Required:    false,
			},
			"url": {
				Type:        "string",
				Description: "Remote URL to the image or document.",
				Required:    false,
			},
			"base64_data": {
				Type:        "string",
				Description: "Base64-encoded file contents when the image is already in memory.",
				Required:    false,
			},
			"mime_type": {
				Type:        "string",
				Description: "Optional MIME type such as image/png or application/pdf.",
				Required:    false,
			},
			"provider": {
				Type:        "string",
				Description: "Optional multimodal provider name override.",
				Required:    false,
			},
			"format": {
				Type:        "string",
				Description: "Optional output format: text or json. Defaults to text.",
				Required:    false,
			},
		},
		Handler: handleImageAnalyze(processor, defaultProvider),
	}
}

type imageAnalyzeOptions struct {
	Path       string
	URL        string
	Base64Data string
	MIMEType   string
	Provider   string
	Format     string
}

func handleImageAnalyze(processor *multimodal.Processor, defaultProvider string) func(args map[string]any) (string, error) {
	return func(args map[string]any) (string, error) {
		if processor == nil {
			return "", fmt.Errorf("image analysis is not configured")
		}

		opts, err := parseImageAnalyzeOptions(args)
		if err != nil {
			return "", err
		}
		input, err := buildImageAnalyzeInput(opts)
		if err != nil {
			return "", err
		}

		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()

		providerName := opts.Provider
		if providerName == "" {
			providerName = strings.TrimSpace(defaultProvider)
		}
		var result *multimodal.AnalysisResult
		if providerName != "" {
			result, err = processor.AnalyzeWithProvider(ctx, providerName, input)
		} else {
			result, err = processor.Analyze(ctx, input)
		}
		if err != nil {
			if providerName != "" {
				return "", fmt.Errorf("image analysis with provider %q for %s input failed: %w", providerName, input.Modality, err)
			}
			return "", fmt.Errorf("image analysis for %s input failed: %w", input.Modality, err)
		}
		if opts.Format == "json" {
			return formatImageAnalysisResultJSON(result)
		}
		return formatImageAnalysisResult(result), nil
	}
}

func parseImageAnalyzeOptions(args map[string]any) (imageAnalyzeOptions, error) {
	path, _ := args["path"].(string)
	url, _ := args["url"].(string)
	base64Data, _ := args["base64_data"].(string)
	mimeType, _ := args["mime_type"].(string)
	provider, _ := args["provider"].(string)
	format, _ := args["format"].(string)

	opts := imageAnalyzeOptions{
		Path:       strings.TrimSpace(path),
		URL:        strings.TrimSpace(url),
		Base64Data: strings.TrimSpace(base64Data),
		MIMEType:   strings.TrimSpace(mimeType),
		Provider:   strings.TrimSpace(provider),
		Format:     strings.ToLower(strings.TrimSpace(format)),
	}
	if opts.Format == "" {
		opts.Format = "text"
	}
	if opts.Format != "text" && opts.Format != "json" {
		return imageAnalyzeOptions{}, fmt.Errorf("unsupported image_analyze format %q; supported formats: text, json", opts.Format)
	}

	inputs := 0
	for _, value := range []string{opts.Path, opts.URL, opts.Base64Data} {
		if value != "" {
			inputs++
		}
	}
	if inputs == 0 {
		return imageAnalyzeOptions{}, fmt.Errorf("one of path, url, or base64_data is required")
	}
	if inputs > 1 {
		return imageAnalyzeOptions{}, fmt.Errorf("path, url, and base64_data are mutually exclusive")
	}
	return opts, nil
}

func buildImageAnalyzeInput(opts imageAnalyzeOptions) (*multimodal.Input, error) {
	mimeType := opts.MIMEType
	modality := inferImageAnalyzeModality(opts.Path, mimeType)
	var input *multimodal.Input
	switch {
	case opts.Path != "":
		if err := validatePath(opts.Path); err != nil {
			return nil, err
		}
		if mimeType == "" {
			mimeType = mime.TypeByExtension(strings.ToLower(filepath.Ext(opts.Path)))
		}
		mimeType = normalizeMediaMIME(mimeType)
		if mimeType == "" {
			detected, err := detectFileMIME(opts.Path)
			if err != nil {
				return nil, err
			}
			mimeType = detected
		}
		modality = inferImageAnalyzeModality(opts.Path, mimeType)
		if err := validateImageAnalyzeMIME(mimeType); err != nil {
			return nil, err
		}
		if err := validateImageAnalyzeFileSize(opts.Path, mimeType); err != nil {
			return nil, err
		}
		input = multimodal.NewInputFromPath(modality, opts.Path)
	case opts.URL != "":
		if err := validateFetchURL(opts.URL); err != nil {
			return nil, fmt.Errorf("url validation failed: %w", err)
		}
		mimeType = normalizeMediaMIME(mimeType)
		if mimeType != "" {
			if err := validateImageAnalyzeMIME(mimeType); err != nil {
				return nil, err
			}
			modality = inferImageAnalyzeModality("", mimeType)
		}
		input = multimodal.NewInputFromURL(modality, opts.URL)
	case opts.Base64Data != "":
		data, err := base64.StdEncoding.DecodeString(opts.Base64Data)
		if err != nil {
			return nil, fmt.Errorf("decode base64_data: %w", err)
		}
		if mimeType == "" {
			mimeType = http.DetectContentType(data)
		}
		mimeType = normalizeMediaMIME(mimeType)
		modality = inferImageAnalyzeModality("", mimeType)
		if err := validateImageAnalyzeMIME(mimeType); err != nil {
			return nil, err
		}
		if err := validateImageAnalyzeBytesSize(int64(len(data)), mimeType); err != nil {
			return nil, err
		}
		input = multimodal.NewInput(modality, mimeType, data)
	}

	if input == nil {
		return nil, fmt.Errorf("failed to build multimodal input")
	}
	input.Modality = modality
	input.MimeType = mimeType
	if input.Metadata == nil {
		input.Metadata = make(map[string]string)
	}
	if opts.Path != "" {
		input.Metadata["file_path"] = opts.Path
		input.Metadata["filename"] = filepath.Base(opts.Path)
	}
	if opts.URL != "" {
		input.Metadata["url"] = opts.URL
	}
	return input, nil
}

func detectFileMIME(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open image_analyze path: %w", err)
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("read image_analyze path: %w", err)
	}
	return normalizeMediaMIME(http.DetectContentType(buf[:n])), nil
}

func validateImageAnalyzeFileSize(path, mimeType string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat image_analyze path: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("image_analyze path is a directory: %s", path)
	}
	return validateImageAnalyzeBytesSize(info.Size(), mimeType)
}

func validateImageAnalyzeBytesSize(size int64, mimeType string) error {
	limit := int64(maxImageAnalyzeImageBytes)
	if normalizeMediaMIME(mimeType) == "application/pdf" {
		limit = maxImageAnalyzeDocBytes
	}
	if size > limit {
		return fmt.Errorf("image_analyze input exceeds %d MiB limit for %s", limit>>20, mimeType)
	}
	return nil
}

func validateImageAnalyzeMIME(mimeType string) error {
	switch normalizeMediaMIME(mimeType) {
	case "image/png", "image/jpeg", "image/webp", "image/gif", "application/pdf":
		return nil
	default:
		return fmt.Errorf("unsupported image_analyze MIME type %q; supported types: image/png, image/jpeg, image/webp, image/gif, application/pdf", mimeType)
	}
}

func normalizeMediaMIME(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	if parsed, _, err := mime.ParseMediaType(value); err == nil {
		value = parsed
	}
	switch value {
	case "image/jpg", "image/pjpeg":
		return "image/jpeg"
	default:
		return value
	}
}

func inferImageAnalyzeModality(path, mimeType string) multimodal.Modality {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	if strings.EqualFold(mimeType, "application/pdf") || strings.EqualFold(filepath.Ext(path), ".pdf") {
		return multimodal.ModalityDocument
	}
	return multimodal.ModalityImage
}

func formatImageAnalysisResult(result *multimodal.AnalysisResult) string {
	if result == nil {
		return "Image analysis unavailable."
	}

	lines := []string{
		fmt.Sprintf("Modality: %s", result.Modality),
	}
	if summary := strings.TrimSpace(result.Summary); summary != "" {
		lines = append(lines, "Summary: "+summary)
	}
	if text := strings.TrimSpace(result.Text); text != "" {
		lines = append(lines, "Visible text / analysis:")
		lines = append(lines, utils.Truncate(text, 4000))
	}
	if len(result.Labels) > 0 {
		lines = append(lines, "Labels: "+strings.Join(result.Labels, ", "))
	}
	if result.Confidence > 0 {
		lines = append(lines, fmt.Sprintf("Confidence: %.2f", result.Confidence))
	}
	if result.Metadata != nil {
		if model := strings.TrimSpace(result.Metadata["model"]); model != "" {
			lines = append(lines, "Model: "+model)
		}
		if source := strings.TrimSpace(result.Metadata["source"]); source != "" {
			lines = append(lines, "Source: "+source)
		}
	}
	return strings.Join(lines, "\n")
}

func formatImageAnalysisResultJSON(result *multimodal.AnalysisResult) (string, error) {
	if result == nil {
		return prettyStructuredValue(map[string]any{
			"available": false,
		})
	}
	text := strings.TrimSpace(result.Text)
	truncatedText := utils.Truncate(text, 4000)
	payload := map[string]any{
		"available":   true,
		"input_id":    result.InputID,
		"modality":    result.Modality,
		"summary":     strings.TrimSpace(result.Summary),
		"text":        truncatedText,
		"text_length": len(text),
		"truncated":   len(truncatedText) < len(text),
		"labels":      result.Labels,
		"confidence":  result.Confidence,
		"duration_ms": result.Duration.Milliseconds(),
		"metadata":    result.Metadata,
	}
	return prettyStructuredValue(payload)
}

// ImageGenerateTool generates images from text prompts and can optionally edit an input image.
func ImageGenerateTool(generator multimodal.ImageGenerator, defaults ImageGenerationDefaults) *Tool {
	return &Tool{
		Name:         "image_generate",
		Description:  "Generate an image from a text prompt, or transform one or more existing images when input_path/input_paths, input_base64_data/input_base64_datas, or input_url/input_urls are provided.",
		Category:     CatBuiltin,
		Source:       "builtin",
		Permission:   PermApprove,
		ShellAware:   true,
		ParallelSafe: false,
		Parameters: map[string]Param{
			"prompt":             {Type: "string", Description: "Text prompt describing the image to generate or the edit you want applied.", Required: true},
			"input_path":         {Type: "string", Description: "Optional local input image path for image-to-image generation.", Required: false},
			"input_paths":        {Type: "array", Description: "Optional list of local input image paths for multi-image generation.", Required: false},
			"input_url":          {Type: "string", Description: "Optional remote input image URL for image-to-image generation.", Required: false},
			"input_urls":         {Type: "array", Description: "Optional list of remote input image URLs for multi-image generation.", Required: false},
			"input_base64_data":  {Type: "string", Description: "Optional base64-encoded input image for image-to-image generation.", Required: false},
			"input_base64_datas": {Type: "array", Description: "Optional list of base64-encoded input images for multi-image generation.", Required: false},
			"input_mime_type":    {Type: "string", Description: "Optional MIME type for base64 input, such as image/png.", Required: false},
			"input_mime_types":   {Type: "array", Description: "Optional list of MIME types aligned with input_base64_datas.", Required: false},
			"model":              {Type: "string", Description: "Optional image generation model override. Defaults to gpt-image-1.5.", Required: false},
			"size":               {Type: "string", Description: "Optional size such as 1024x1024, 1536x1024, 1024x1536, or auto.", Required: false},
			"quality":            {Type: "string", Description: "Optional quality such as low, medium, high, or auto.", Required: false},
			"background":         {Type: "string", Description: "Optional background mode such as auto, opaque, or transparent.", Required: false},
			"output_format":      {Type: "string", Description: "Optional output format: png, jpeg, or webp. Defaults to png.", Required: false},
			"output_compression": {Type: "number", Description: "Optional output compression for jpeg/webp, from 0 to 100.", Required: false},
			"count":              {Type: "number", Description: "Optional number of images to generate. Defaults to 1.", Required: false},
			"output_path":        {Type: "string", Description: "Optional destination file path for a single generated image. Must stay under ~/.luckyagent/workspace; relative values are resolved there.", Required: false},
			"output_dir":         {Type: "string", Description: "Optional destination directory. Defaults to ~/.luckyagent/workspace/generated-images. Explicit values must stay under ~/.luckyagent/workspace; relative values are resolved there.", Required: false},
			"filename_prefix":    {Type: "string", Description: "Optional output filename prefix when output_dir is used.", Required: false},
		},
		Handler: handleImageGenerate(generator, defaults),
	}
}

func handleImageGenerate(generator multimodal.ImageGenerator, defaults ImageGenerationDefaults) func(args map[string]any) (string, error) {
	return func(args map[string]any) (string, error) {
		if generator == nil {
			return "", fmt.Errorf("image generation is not configured")
		}

		req, outputPath, outputDir, filenamePrefix, baseDir, err := buildImageGenerationRequest(args, defaults)
		if err != nil {
			return "", err
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		result, err := generator.GenerateImage(ctx, *req)
		if err != nil {
			return "", err
		}
		if result == nil || len(result.Images) == 0 {
			return "", fmt.Errorf("image generation returned no images")
		}

		savedPaths, err := saveGeneratedImages(result.Images, outputPath, outputDir, filenamePrefix, baseDir)
		if err != nil {
			return "", err
		}

		payload := map[string]any{
			"provider":       result.Provider,
			"model":          result.Model,
			"count":          len(savedPaths),
			"paths":          savedPaths,
			"revised_prompt": result.RevisedPrompt,
		}
		if !result.CreatedAt.IsZero() {
			payload["created_at"] = result.CreatedAt.Format(time.RFC3339)
		}
		if result.Metadata != nil && len(result.Metadata) > 0 {
			payload["metadata"] = result.Metadata
		}
		return prettyStructuredValue(payload)
	}
}

func buildImageGenerationRequest(args map[string]any, defaults ImageGenerationDefaults) (*multimodal.ImageGenerationRequest, string, string, string, string, error) {
	prompt, _ := args["prompt"].(string)
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, "", "", "", "", fmt.Errorf("prompt is required")
	}

	inputPaths := collectStringArgs(args, "input_path", "input_paths")
	inputURLs := collectStringArgs(args, "input_url", "input_urls")
	inputBase64s := collectStringArgs(args, "input_base64_data", "input_base64_datas")
	inputMimeType, _ := args["input_mime_type"].(string)
	inputMimeTypes := collectStringArgs(args, "input_mime_types")
	inputMimeType = strings.TrimSpace(inputMimeType)
	if inputMimeType != "" && len(inputMimeTypes) == 0 {
		inputMimeTypes = []string{inputMimeType}
	}

	outputPath, _ := args["output_path"].(string)
	outputDir, _ := args["output_dir"].(string)
	filenamePrefix, _ := args["filename_prefix"].(string)
	baseDir, _ := args["_cwd"].(string)
	outputPath = strings.TrimSpace(outputPath)
	outputDir = strings.TrimSpace(outputDir)
	filenamePrefix = strings.TrimSpace(filenamePrefix)
	baseDir = strings.TrimSpace(baseDir)

	defaultCount := defaults.Count
	if defaultCount <= 0 {
		defaultCount = 1
	}
	count := boundedIntArg(args, "count", defaultCount, 1, 10)
	if outputPath != "" && count > 1 {
		return nil, "", "", "", "", fmt.Errorf("output_path can only be used when count is 1")
	}

	req := &multimodal.ImageGenerationRequest{
		Prompt:            prompt,
		Model:             firstNonEmptyString(asString(args["model"]), defaults.Model),
		Size:              firstNonEmptyString(asString(args["size"]), defaults.Size),
		Quality:           firstNonEmptyString(asString(args["quality"]), defaults.Quality),
		Background:        firstNonEmptyString(asString(args["background"]), defaults.Background),
		OutputFormat:      normalizeOutputFormat(firstNonEmptyString(asString(args["output_format"]), defaults.OutputFormat)),
		OutputCompression: boundedIntArg(args, "output_compression", defaults.OutputCompression, 0, 100),
		Count:             count,
	}

	for _, inputPath := range inputPaths {
		inputPath = resolveToolPath(baseDir, inputPath)
		if err := validatePath(inputPath); err != nil {
			return nil, "", "", "", "", err
		}
		data, err := os.ReadFile(inputPath)
		if err != nil {
			return nil, "", "", "", "", fmt.Errorf("read input_path: %w", err)
		}
		pathMimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(inputPath)))
		if pathMimeType == "" {
			pathMimeType = http.DetectContentType(data)
		}
		req.InputImages = append(req.InputImages, multimodal.ImageInput{
			Data:     data,
			MimeType: pathMimeType,
			Filename: filepath.Base(inputPath),
		})
	}

	for _, inputURL := range inputURLs {
		data, detectedMimeType, err := downloadImageInput(inputURL)
		if err != nil {
			return nil, "", "", "", "", err
		}
		req.InputImages = append(req.InputImages, multimodal.ImageInput{
			Data:     data,
			MimeType: detectedMimeType,
			Filename: filepath.Base(strings.Split(inputURL, "?")[0]),
		})
	}

	for i, inputBase64 := range inputBase64s {
		data, err := base64.StdEncoding.DecodeString(inputBase64)
		if err != nil {
			return nil, "", "", "", "", fmt.Errorf("decode input_base64_data: %w", err)
		}
		currentMimeType := ""
		if i < len(inputMimeTypes) {
			currentMimeType = strings.TrimSpace(inputMimeTypes[i])
		}
		if currentMimeType == "" {
			currentMimeType = http.DetectContentType(data)
		}
		req.InputImages = append(req.InputImages, multimodal.ImageInput{
			Data:     data,
			MimeType: currentMimeType,
			Filename: fmt.Sprintf("input-%02d%s", i+1, extensionForOutputFormat(currentMimeType)),
		})
	}

	return req, outputPath, outputDir, filenamePrefix, baseDir, nil
}

func saveGeneratedImages(images []multimodal.GeneratedImage, outputPath, outputDir, filenamePrefix, baseDir string) ([]string, error) {
	if len(images) == 0 {
		return nil, fmt.Errorf("no images to save")
	}

	if filenamePrefix == "" {
		filenamePrefix = "generated-image"
	}

	if outputPath != "" {
		resolved, err := validateResolvedOutputPath(baseDir, outputPath)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
			return nil, fmt.Errorf("create output directory: %w", err)
		}
		if err := os.WriteFile(resolved, images[0].Data, 0o644); err != nil {
			return nil, fmt.Errorf("write output file: %w", err)
		}
		return []string{resolved}, nil
	}

	dir, err := resolveImageOutputDir(baseDir, outputDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create output_dir: %w", err)
	}

	var saved []string
	for i, image := range images {
		filename := fmt.Sprintf("%s-%02d%s", filenamePrefix, i+1, extensionForOutputFormat(image.MimeType))
		path := filepath.Join(dir, filename)
		resolved, err := resolveWorkspacePath(path)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(resolved, image.Data, 0o644); err != nil {
			return nil, fmt.Errorf("write generated image: %w", err)
		}
		saved = append(saved, resolved)
	}
	return saved, nil
}

func validateResolvedOutputPath(_ string, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("output path is empty")
	}
	return resolveWorkspacePath(path)
}

func resolveImageOutputDir(baseDir, outputDir string) (string, error) {
	if outputDir != "" {
		return validateResolvedOutputPath(baseDir, outputDir)
	}

	dir := filepath.Join(sandboxWorkspaceDir(), "generated-images")
	return resolveWorkspacePath(dir)
}

func resolveToolPath(baseDir, path string) string {
	if path == "" || filepath.IsAbs(path) || strings.TrimSpace(baseDir) == "" {
		return path
	}
	return filepath.Join(baseDir, path)
}

func downloadImageInput(rawURL string) ([]byte, string, error) {
	if err := validateFetchURL(rawURL); err != nil {
		return nil, "", fmt.Errorf("input_url validation failed: %w", err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create input_url request: %w", err)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download input_url: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("download input_url failed with status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return nil, "", fmt.Errorf("read input_url response: %w", err)
	}
	mimeType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	return data, mimeType, nil
}

func normalizeOutputFormat(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "png":
		return "png"
	case "jpg", "jpeg":
		return "jpeg"
	case "webp":
		return "webp"
	default:
		return format
	}
}

func extensionForOutputFormat(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "jpeg", "jpg", "image/jpeg":
		return ".jpg"
	case "webp", "image/webp":
		return ".webp"
	default:
		return ".png"
	}
}

func asString(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func collectStringArgs(args map[string]any, keys ...string) []string {
	var out []string
	for _, key := range keys {
		raw, ok := args[key]
		if !ok || raw == nil {
			continue
		}
		switch v := raw.(type) {
		case string:
			if trimmed := strings.TrimSpace(v); trimmed != "" {
				out = append(out, trimmed)
			}
		case []string:
			for _, item := range v {
				if trimmed := strings.TrimSpace(item); trimmed != "" {
					out = append(out, trimmed)
				}
			}
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok {
					if trimmed := strings.TrimSpace(s); trimmed != "" {
						out = append(out, trimmed)
					}
				}
			}
		}
	}
	return out
}

// TextToSpeechTool synthesizes speech audio from text and saves it to disk.
func TextToSpeechTool(synthesizer multimodal.SpeechSynthesizer, defaults TTSDefaults) *Tool {
	return &Tool{
		Name:         "text_to_speech",
		Description:  "Generate a speech audio file from input text. Useful for voiceovers, spoken summaries, and audio delivery.",
		Category:     CatBuiltin,
		Source:       "builtin",
		Permission:   PermApprove,
		ShellAware:   true,
		ParallelSafe: false,
		Parameters: map[string]Param{
			"text":            {Type: "string", Description: "Text that should be spoken in the synthesized audio output.", Required: true},
			"model":           {Type: "string", Description: "Optional TTS model override.", Required: false},
			"voice":           {Type: "string", Description: "Optional voice name such as alloy, nova, shimmer, or a provider-specific voice ID.", Required: false},
			"format":          {Type: "string", Description: "Optional audio format such as mp3, wav, opus, aac, or flac.", Required: false},
			"speed":           {Type: "number", Description: "Optional playback speed multiplier. Defaults to 1.0.", Required: false},
			"output_path":     {Type: "string", Description: "Optional destination file path for the generated audio. Must stay under ~/.luckyagent/workspace; relative values are resolved there.", Required: false},
			"output_dir":      {Type: "string", Description: "Optional destination directory. Defaults to ~/.luckyagent/workspace/generated-audio. Explicit values must stay under ~/.luckyagent/workspace; relative values are resolved there.", Required: false},
			"filename_prefix": {Type: "string", Description: "Optional output filename prefix when output_dir is used.", Required: false},
		},
		Handler: handleTextToSpeech(synthesizer, defaults),
	}
}

func handleTextToSpeech(synthesizer multimodal.SpeechSynthesizer, defaults TTSDefaults) func(args map[string]any) (string, error) {
	return func(args map[string]any) (string, error) {
		if synthesizer == nil {
			return "", fmt.Errorf("text-to-speech is not configured")
		}
		req, outputPath, outputDir, filenamePrefix, baseDir, err := buildSpeechSynthesisRequest(args, defaults)
		if err != nil {
			return "", err
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		result, err := synthesizer.SynthesizeSpeech(ctx, *req)
		if err != nil {
			return "", err
		}
		if result == nil || len(result.Audio) == 0 {
			return "", fmt.Errorf("text-to-speech returned no audio")
		}

		savedPath, err := saveSynthesizedAudio(result, outputPath, outputDir, filenamePrefix, baseDir)
		if err != nil {
			return "", err
		}

		payload := map[string]any{
			"provider": result.Provider,
			"model":    result.Model,
			"voice":    result.Voice,
			"path":     savedPath,
			"format":   speechFormatFromMimeType(result.MimeType),
		}
		if !result.CreatedAt.IsZero() {
			payload["created_at"] = result.CreatedAt.Format(time.RFC3339)
		}
		if result.Metadata != nil && len(result.Metadata) > 0 {
			payload["metadata"] = result.Metadata
		}
		return prettyStructuredValue(payload)
	}
}

func buildSpeechSynthesisRequest(args map[string]any, defaults TTSDefaults) (*multimodal.SpeechSynthesisRequest, string, string, string, string, error) {
	text, _ := args["text"].(string)
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, "", "", "", "", fmt.Errorf("text is required")
	}

	outputPath, _ := args["output_path"].(string)
	outputDir, _ := args["output_dir"].(string)
	filenamePrefix, _ := args["filename_prefix"].(string)
	baseDir, _ := args["_cwd"].(string)

	req := &multimodal.SpeechSynthesisRequest{
		Text:   text,
		Model:  firstNonEmptyString(asString(args["model"]), defaults.Model),
		Voice:  firstNonEmptyString(asString(args["voice"]), defaults.Voice),
		Format: normalizeTTSFormat(firstNonEmptyString(asString(args["format"]), defaults.Format)),
		Speed:  speechSpeedArg(args, defaults.Speed),
	}
	return req, strings.TrimSpace(outputPath), strings.TrimSpace(outputDir), strings.TrimSpace(filenamePrefix), strings.TrimSpace(baseDir), nil
}

func saveSynthesizedAudio(result *multimodal.SpeechSynthesisResult, outputPath, outputDir, filenamePrefix, baseDir string) (string, error) {
	if result == nil || len(result.Audio) == 0 {
		return "", fmt.Errorf("no synthesized audio to save")
	}
	if filenamePrefix == "" {
		filenamePrefix = fmt.Sprintf("tts-audio-%d", time.Now().UnixNano())
	}
	if outputPath != "" {
		resolved, err := validateResolvedOutputPath(baseDir, outputPath)
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
			return "", fmt.Errorf("create output directory: %w", err)
		}
		if err := os.WriteFile(resolved, result.Audio, 0o644); err != nil {
			return "", fmt.Errorf("write output file: %w", err)
		}
		return resolved, nil
	}

	dir, err := resolveGeneratedAudioDir(baseDir, outputDir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create output_dir: %w", err)
	}

	filename := fmt.Sprintf("%s%s", filenamePrefix, extensionForSpeechFormat(result.MimeType))
	path := filepath.Join(dir, filename)
	resolved, err := resolveWorkspacePath(path)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(resolved, result.Audio, 0o644); err != nil {
		return "", fmt.Errorf("write synthesized audio: %w", err)
	}
	return resolved, nil
}

func resolveGeneratedAudioDir(baseDir, outputDir string) (string, error) {
	if outputDir != "" {
		return validateResolvedOutputPath(baseDir, outputDir)
	}
	dir := filepath.Join(sandboxWorkspaceDir(), "generated-audio")
	return resolveWorkspacePath(dir)
}

func speechSpeedArg(args map[string]any, def float64) float64 {
	if def <= 0 {
		def = 1.0
	}
	if raw, ok := args["speed"]; ok {
		switch v := raw.(type) {
		case float64:
			if v > 0 {
				return v
			}
		case int:
			if v > 0 {
				return float64(v)
			}
		}
	}
	return def
}

func extensionForSpeechFormat(value string) string {
	switch normalizeTTSFormat(value) {
	case "wav", "audio/wav":
		return ".wav"
	case "opus", "audio/opus":
		return ".opus"
	case "aac", "audio/aac":
		return ".aac"
	case "flac", "audio/flac":
		return ".flac"
	case "pcm", "audio/pcm":
		return ".pcm"
	default:
		return ".mp3"
	}
}

func normalizeTTSFormat(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "mp3", "audio/mpeg":
		return "mp3"
	case "wav", "audio/wav":
		return "wav"
	case "opus", "audio/opus", "ogg", "audio/ogg":
		return "opus"
	case "aac", "audio/aac":
		return "aac"
	case "flac", "audio/flac":
		return "flac"
	case "pcm", "pcm16", "audio/pcm":
		return "pcm"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func speechFormatFromMimeType(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "audio/wav":
		return "wav"
	case "audio/opus":
		return "opus"
	case "audio/aac":
		return "aac"
	case "audio/flac":
		return "flac"
	case "audio/pcm":
		return "pcm"
	default:
		return "mp3"
	}
}
