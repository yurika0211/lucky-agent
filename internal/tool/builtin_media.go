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

	maxImagePromptChars              = 8000
	maxImageGenerateInputs           = 8
	maxImageGenerateInputBytes int64 = 20 << 20
	maxImageGenerateTotalBytes int64 = 50 << 20

	maxTTSInputChars = 12000
	minTTSSpeed      = 0.25
	maxTTSSpeed      = 4.0
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
			"overwrite":          {Type: "boolean", Description: "Allow replacing an existing output_path. Defaults to false.", Required: false},
			"dry_run":            {Type: "boolean", Description: "Return the generation plan without calling the provider or writing files.", Required: false},
		},
		Handler: handleImageGenerate(generator, defaults),
	}
}

type imageGenerationOptions struct {
	Prompt            string
	InputPaths        []string
	InputURLs         []string
	InputBase64s      []string
	InputMIMETypes    []string
	Model             string
	Size              string
	Quality           string
	Background        string
	OutputFormat      string
	OutputCompression int
	Count             int
	OutputPath        string
	OutputDir         string
	FilenamePrefix    string
	Overwrite         bool
	DryRun            bool
	BaseDir           string
}

type imageGenerationPlan struct {
	Provider      string   `json:"provider,omitempty"`
	Model         string   `json:"model,omitempty"`
	Size          string   `json:"size,omitempty"`
	Quality       string   `json:"quality,omitempty"`
	Background    string   `json:"background,omitempty"`
	OutputFormat  string   `json:"output_format,omitempty"`
	Count         int      `json:"count"`
	InputCount    int      `json:"input_count"`
	InputBytes    int64    `json:"input_bytes"`
	OutputTargets []string `json:"output_targets"`
	Overwrite     bool     `json:"overwrite"`
	DryRun        bool     `json:"dry_run"`
}

func handleImageGenerate(generator multimodal.ImageGenerator, defaults ImageGenerationDefaults) func(args map[string]any) (string, error) {
	return func(args map[string]any) (string, error) {
		opts, err := parseImageGenerationOptions(args, defaults)
		if err != nil {
			return "", err
		}
		providerName := ""
		if generator != nil {
			providerName = generator.Name()
		}
		plan, err := buildImageGenerationPlan(opts, providerName)
		if err != nil {
			return "", err
		}
		if opts.DryRun {
			return prettyStructuredValue(plan)
		}
		if generator == nil {
			return "", fmt.Errorf("image generation is not configured")
		}
		if err := validateImageOutputConflicts(opts); err != nil {
			return "", err
		}

		req, err := buildImageGenerationRequest(opts)
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

		savedPaths, err := saveGeneratedImages(result.Images, opts)
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

func parseImageGenerationOptions(args map[string]any, defaults ImageGenerationDefaults) (imageGenerationOptions, error) {
	prompt, _ := args["prompt"].(string)
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return imageGenerationOptions{}, fmt.Errorf("prompt is required")
	}
	if len([]rune(prompt)) > maxImagePromptChars {
		return imageGenerationOptions{}, fmt.Errorf("prompt exceeds %d character limit", maxImagePromptChars)
	}

	inputPaths := collectStringArgs(args, "input_path", "input_paths")
	inputURLs := collectStringArgs(args, "input_url", "input_urls")
	inputBase64s := collectStringArgs(args, "input_base64_data", "input_base64_datas")
	if len(inputPaths)+len(inputURLs)+len(inputBase64s) > maxImageGenerateInputs {
		return imageGenerationOptions{}, fmt.Errorf("image_generate supports at most %d input images", maxImageGenerateInputs)
	}
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
		return imageGenerationOptions{}, fmt.Errorf("output_path can only be used when count is 1")
	}

	outputFormat, err := validateImageOutputFormat(firstNonEmptyString(asString(args["output_format"]), defaults.OutputFormat))
	if err != nil {
		return imageGenerationOptions{}, err
	}
	if filenamePrefix != "" {
		if err := validateFilenamePrefix(filenamePrefix, "filename_prefix"); err != nil {
			return imageGenerationOptions{}, err
		}
	}

	return imageGenerationOptions{
		Prompt:            prompt,
		InputPaths:        inputPaths,
		InputURLs:         inputURLs,
		InputBase64s:      inputBase64s,
		InputMIMETypes:    inputMimeTypes,
		Model:             firstNonEmptyString(asString(args["model"]), defaults.Model),
		Size:              firstNonEmptyString(asString(args["size"]), defaults.Size),
		Quality:           firstNonEmptyString(asString(args["quality"]), defaults.Quality),
		Background:        firstNonEmptyString(asString(args["background"]), defaults.Background),
		OutputFormat:      outputFormat,
		OutputCompression: boundedIntArg(args, "output_compression", defaults.OutputCompression, 0, 100),
		Count:             count,
		OutputPath:        outputPath,
		OutputDir:         outputDir,
		FilenamePrefix:    filenamePrefix,
		Overwrite:         mediaBoolArg(args, "overwrite", false),
		DryRun:            mediaBoolArg(args, "dry_run", false),
		BaseDir:           baseDir,
	}, nil
}

func buildImageGenerationPlan(opts imageGenerationOptions, providerName string) (imageGenerationPlan, error) {
	targets, err := plannedImageOutputTargets(opts)
	if err != nil {
		return imageGenerationPlan{}, err
	}
	inputBytes, err := estimateImageInputBytes(opts)
	if err != nil {
		return imageGenerationPlan{}, err
	}
	return imageGenerationPlan{
		Provider:      providerName,
		Model:         opts.Model,
		Size:          opts.Size,
		Quality:       opts.Quality,
		Background:    opts.Background,
		OutputFormat:  opts.OutputFormat,
		Count:         opts.Count,
		InputCount:    len(opts.InputPaths) + len(opts.InputURLs) + len(opts.InputBase64s),
		InputBytes:    inputBytes,
		OutputTargets: targets,
		Overwrite:     opts.Overwrite,
		DryRun:        opts.DryRun,
	}, nil
}

func buildImageGenerationRequest(opts imageGenerationOptions) (*multimodal.ImageGenerationRequest, error) {
	req := &multimodal.ImageGenerationRequest{
		Prompt:            opts.Prompt,
		Model:             opts.Model,
		Size:              opts.Size,
		Quality:           opts.Quality,
		Background:        opts.Background,
		OutputFormat:      opts.OutputFormat,
		OutputCompression: opts.OutputCompression,
		Count:             opts.Count,
	}

	var totalBytes int64
	for _, inputPath := range opts.InputPaths {
		inputPath = resolveToolPath(opts.BaseDir, inputPath)
		if err := validatePath(inputPath); err != nil {
			return nil, err
		}
		info, err := os.Stat(inputPath)
		if err != nil {
			return nil, fmt.Errorf("stat input_path: %w", err)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("input_path is a directory: %s", inputPath)
		}
		if err := validateImageGenerateInputSize(info.Size(), &totalBytes); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(inputPath)
		if err != nil {
			return nil, fmt.Errorf("read input_path: %w", err)
		}
		pathMimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(inputPath)))
		if pathMimeType == "" {
			pathMimeType = http.DetectContentType(data)
		}
		pathMimeType = normalizeMediaMIME(pathMimeType)
		if err := validateImageGenerateMIME(pathMimeType); err != nil {
			return nil, err
		}
		req.InputImages = append(req.InputImages, multimodal.ImageInput{
			Data:     data,
			MimeType: pathMimeType,
			Filename: filepath.Base(inputPath),
		})
	}

	for _, inputURL := range opts.InputURLs {
		data, detectedMimeType, err := downloadImageInput(inputURL)
		if err != nil {
			return nil, err
		}
		if err := validateImageGenerateInputSize(int64(len(data)), &totalBytes); err != nil {
			return nil, err
		}
		if err := validateImageGenerateMIME(detectedMimeType); err != nil {
			return nil, err
		}
		req.InputImages = append(req.InputImages, multimodal.ImageInput{
			Data:     data,
			MimeType: detectedMimeType,
			Filename: filepath.Base(strings.Split(inputURL, "?")[0]),
		})
	}

	for i, inputBase64 := range opts.InputBase64s {
		data, err := base64.StdEncoding.DecodeString(inputBase64)
		if err != nil {
			return nil, fmt.Errorf("decode input_base64_data: %w", err)
		}
		if err := validateImageGenerateInputSize(int64(len(data)), &totalBytes); err != nil {
			return nil, err
		}
		currentMimeType := ""
		if i < len(opts.InputMIMETypes) {
			currentMimeType = strings.TrimSpace(opts.InputMIMETypes[i])
		}
		if currentMimeType == "" {
			currentMimeType = http.DetectContentType(data)
		}
		currentMimeType = normalizeMediaMIME(currentMimeType)
		if err := validateImageGenerateMIME(currentMimeType); err != nil {
			return nil, err
		}
		req.InputImages = append(req.InputImages, multimodal.ImageInput{
			Data:     data,
			MimeType: currentMimeType,
			Filename: fmt.Sprintf("input-%02d%s", i+1, extensionForOutputFormat(currentMimeType)),
		})
	}

	return req, nil
}

func saveGeneratedImages(images []multimodal.GeneratedImage, opts imageGenerationOptions) ([]string, error) {
	if len(images) == 0 {
		return nil, fmt.Errorf("no images to save")
	}

	filenamePrefix := opts.FilenamePrefix
	if filenamePrefix == "" {
		filenamePrefix = "generated-image"
	}

	if opts.OutputPath != "" {
		resolved, err := validateResolvedOutputPath(opts.BaseDir, opts.OutputPath)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
			return nil, fmt.Errorf("create output directory: %w", err)
		}
		if !opts.Overwrite {
			if _, err := os.Stat(resolved); err == nil {
				return nil, fmt.Errorf("output_path already exists: %s", resolved)
			} else if !os.IsNotExist(err) {
				return nil, fmt.Errorf("stat output_path: %w", err)
			}
		}
		if err := mediaWriteFileAtomic(resolved, images[0].Data, 0o644); err != nil {
			return nil, fmt.Errorf("write output file: %w", err)
		}
		return []string{resolved}, nil
	}

	dir, err := resolveImageOutputDir(opts.BaseDir, opts.OutputDir)
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
		if !opts.Overwrite {
			resolved, err = uniqueOutputPath(resolved)
			if err != nil {
				return nil, err
			}
		}
		if err := mediaWriteFileAtomic(resolved, image.Data, 0o644); err != nil {
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

func plannedImageOutputTargets(opts imageGenerationOptions) ([]string, error) {
	if opts.OutputPath != "" {
		resolved, err := validateResolvedOutputPath(opts.BaseDir, opts.OutputPath)
		if err != nil {
			return nil, err
		}
		return []string{resolved}, nil
	}
	dir, err := resolveImageOutputDir(opts.BaseDir, opts.OutputDir)
	if err != nil {
		return nil, err
	}
	filenamePrefix := opts.FilenamePrefix
	if filenamePrefix == "" {
		filenamePrefix = "generated-image"
	}
	ext := extensionForOutputFormat(opts.OutputFormat)
	targets := make([]string, 0, opts.Count)
	for i := 0; i < opts.Count; i++ {
		resolved, err := resolveWorkspacePath(filepath.Join(dir, fmt.Sprintf("%s-%02d%s", filenamePrefix, i+1, ext)))
		if err != nil {
			return nil, err
		}
		targets = append(targets, resolved)
	}
	return targets, nil
}

func validateImageOutputConflicts(opts imageGenerationOptions) error {
	if opts.OutputPath == "" || opts.Overwrite {
		return nil
	}
	resolved, err := validateResolvedOutputPath(opts.BaseDir, opts.OutputPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(resolved); err == nil {
		return fmt.Errorf("output_path already exists: %s", resolved)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat output_path: %w", err)
	}
	return nil
}

func estimateImageInputBytes(opts imageGenerationOptions) (int64, error) {
	var total int64
	for _, inputPath := range opts.InputPaths {
		resolved := resolveToolPath(opts.BaseDir, inputPath)
		if err := validatePath(resolved); err != nil {
			return 0, err
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return 0, fmt.Errorf("stat input_path: %w", err)
		}
		if info.IsDir() {
			return 0, fmt.Errorf("input_path is a directory: %s", resolved)
		}
		if err := validateImageGenerateInputSize(info.Size(), &total); err != nil {
			return 0, err
		}
	}
	for _, inputBase64 := range opts.InputBase64s {
		size := int64(base64.StdEncoding.DecodedLen(len(inputBase64)))
		if err := validateImageGenerateInputSize(size, &total); err != nil {
			return 0, err
		}
	}
	return total, nil
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
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageGenerateInputBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("read input_url response: %w", err)
	}
	if int64(len(data)) > maxImageGenerateInputBytes {
		return nil, "", fmt.Errorf("input image exceeds %d MiB limit", maxImageGenerateInputBytes>>20)
	}
	mimeType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	mimeType = normalizeMediaMIME(mimeType)
	return data, mimeType, nil
}

func validateImageGenerateInputSize(size int64, total *int64) error {
	if size > maxImageGenerateInputBytes {
		return fmt.Errorf("input image exceeds %d MiB limit", maxImageGenerateInputBytes>>20)
	}
	if total != nil {
		*total += size
		if *total > maxImageGenerateTotalBytes {
			return fmt.Errorf("input images exceed %d MiB total limit", maxImageGenerateTotalBytes>>20)
		}
	}
	return nil
}

func validateImageGenerateMIME(mimeType string) error {
	switch normalizeMediaMIME(mimeType) {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		return nil
	default:
		return fmt.Errorf("unsupported image input MIME type %q; supported types: image/png, image/jpeg, image/webp, image/gif", mimeType)
	}
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

func validateImageOutputFormat(format string) (string, error) {
	normalized := normalizeOutputFormat(format)
	switch normalized {
	case "png", "jpeg", "webp":
		return normalized, nil
	default:
		return "", fmt.Errorf("unsupported output_format %q; supported formats: png, jpeg, webp", format)
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

func mediaBoolArg(args map[string]any, key string, def bool) bool {
	raw, ok := args[key]
	if !ok {
		return def
	}
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1", "yes", "y":
			return true
		case "false", "0", "no", "n":
			return false
		}
	}
	return def
}

func validateFilenamePrefix(prefix, param string) error {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return fmt.Errorf("%s is empty", param)
	}
	if prefix == "." || prefix == ".." || strings.Contains(prefix, "..") {
		return fmt.Errorf("%s must be a filename fragment, not a relative path", param)
	}
	if strings.ContainsAny(prefix, `/\`) {
		return fmt.Errorf("%s must not contain path separators", param)
	}
	if len([]rune(prefix)) > 80 {
		return fmt.Errorf("%s exceeds 80 character limit", param)
	}
	for _, r := range prefix {
		if r < 32 || r == 127 {
			return fmt.Errorf("%s must not contain control characters", param)
		}
	}
	return nil
}

func uniqueOutputPath(path string) (string, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path, nil
	} else if err != nil {
		return "", fmt.Errorf("stat output path: %w", err)
	}
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	for i := 0; i < 1000; i++ {
		candidate := fmt.Sprintf("%s-%d-%03d%s", stem, time.Now().UnixNano(), i, ext)
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate, nil
		} else if err != nil {
			return "", fmt.Errorf("stat output path: %w", err)
		}
	}
	return "", fmt.Errorf("failed to allocate unique output path for %s", path)
}

func mediaWriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
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
			"overwrite":       {Type: "boolean", Description: "Allow replacing an existing output file. Defaults to false.", Required: false},
			"dry_run":         {Type: "boolean", Description: "Return the synthesis plan without calling the provider or writing files.", Required: false},
			"allow_custom_format": {
				Type:        "boolean",
				Description: "Allow provider-specific audio formats outside the built-in allowlist.",
				Required:    false,
			},
		},
		Handler: handleTextToSpeech(synthesizer, defaults),
	}
}

type speechSynthesisOptions struct {
	Text              string
	Model             string
	Voice             string
	Format            string
	Speed             float64
	OutputPath        string
	OutputDir         string
	FilenamePrefix    string
	Overwrite         bool
	DryRun            bool
	AllowCustomFormat bool
	BaseDir           string
}

type speechSynthesisPlan struct {
	Provider     string  `json:"provider,omitempty"`
	Model        string  `json:"model,omitempty"`
	Voice        string  `json:"voice,omitempty"`
	Format       string  `json:"format"`
	Speed        float64 `json:"speed"`
	TextChars    int     `json:"text_chars"`
	OutputTarget string  `json:"output_target"`
	Overwrite    bool    `json:"overwrite"`
	DryRun       bool    `json:"dry_run"`
}

func handleTextToSpeech(synthesizer multimodal.SpeechSynthesizer, defaults TTSDefaults) func(args map[string]any) (string, error) {
	return func(args map[string]any) (string, error) {
		opts, err := parseSpeechSynthesisOptions(args, defaults)
		if err != nil {
			return "", err
		}
		providerName := ""
		if synthesizer != nil {
			providerName = synthesizer.Name()
		}
		plan, err := buildSpeechSynthesisPlan(opts, providerName)
		if err != nil {
			return "", err
		}
		if opts.DryRun {
			return prettyStructuredValue(plan)
		}
		if synthesizer == nil {
			return "", fmt.Errorf("text-to-speech is not configured")
		}
		if err := validateSpeechOutputConflicts(opts); err != nil {
			return "", err
		}
		req := buildSpeechSynthesisRequest(opts)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		result, err := synthesizer.SynthesizeSpeech(ctx, *req)
		if err != nil {
			return "", err
		}
		if result == nil || len(result.Audio) == 0 {
			return "", fmt.Errorf("text-to-speech returned no audio")
		}

		savedPath, err := saveSynthesizedAudio(result, opts)
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

func parseSpeechSynthesisOptions(args map[string]any, defaults TTSDefaults) (speechSynthesisOptions, error) {
	text, _ := args["text"].(string)
	text = strings.TrimSpace(text)
	if text == "" {
		return speechSynthesisOptions{}, fmt.Errorf("text is required")
	}
	if len([]rune(text)) > maxTTSInputChars {
		return speechSynthesisOptions{}, fmt.Errorf("text exceeds %d character limit; split it into smaller requests", maxTTSInputChars)
	}

	outputPath, _ := args["output_path"].(string)
	outputDir, _ := args["output_dir"].(string)
	filenamePrefix, _ := args["filename_prefix"].(string)
	baseDir, _ := args["_cwd"].(string)
	outputPath = strings.TrimSpace(outputPath)
	outputDir = strings.TrimSpace(outputDir)
	filenamePrefix = strings.TrimSpace(filenamePrefix)
	baseDir = strings.TrimSpace(baseDir)
	if filenamePrefix != "" {
		if err := validateFilenamePrefix(filenamePrefix, "filename_prefix"); err != nil {
			return speechSynthesisOptions{}, err
		}
	}

	model := firstNonEmptyString(asString(args["model"]), defaults.Model)
	voice := firstNonEmptyString(asString(args["voice"]), defaults.Voice)
	if err := validateProviderIDString("model", model); err != nil {
		return speechSynthesisOptions{}, err
	}
	if err := validateProviderIDString("voice", voice); err != nil {
		return speechSynthesisOptions{}, err
	}
	allowCustomFormat := mediaBoolArg(args, "allow_custom_format", false)
	format, err := validateTTSFormat(firstNonEmptyString(asString(args["format"]), defaults.Format), allowCustomFormat)
	if err != nil {
		return speechSynthesisOptions{}, err
	}
	speed, err := speechSpeedArg(args, defaults.Speed)
	if err != nil {
		return speechSynthesisOptions{}, err
	}

	return speechSynthesisOptions{
		Text:              text,
		Model:             model,
		Voice:             voice,
		Format:            format,
		Speed:             speed,
		OutputPath:        outputPath,
		OutputDir:         outputDir,
		FilenamePrefix:    filenamePrefix,
		Overwrite:         mediaBoolArg(args, "overwrite", false),
		DryRun:            mediaBoolArg(args, "dry_run", false),
		AllowCustomFormat: allowCustomFormat,
		BaseDir:           baseDir,
	}, nil
}

func buildSpeechSynthesisRequest(opts speechSynthesisOptions) *multimodal.SpeechSynthesisRequest {
	return &multimodal.SpeechSynthesisRequest{
		Text:   opts.Text,
		Model:  opts.Model,
		Voice:  opts.Voice,
		Format: opts.Format,
		Speed:  opts.Speed,
	}
}

func buildSpeechSynthesisPlan(opts speechSynthesisOptions, providerName string) (speechSynthesisPlan, error) {
	target, err := plannedSpeechOutputTarget(opts)
	if err != nil {
		return speechSynthesisPlan{}, err
	}
	return speechSynthesisPlan{
		Provider:     providerName,
		Model:        opts.Model,
		Voice:        opts.Voice,
		Format:       opts.Format,
		Speed:        opts.Speed,
		TextChars:    len([]rune(opts.Text)),
		OutputTarget: target,
		Overwrite:    opts.Overwrite,
		DryRun:       opts.DryRun,
	}, nil
}

func saveSynthesizedAudio(result *multimodal.SpeechSynthesisResult, opts speechSynthesisOptions) (string, error) {
	if result == nil || len(result.Audio) == 0 {
		return "", fmt.Errorf("no synthesized audio to save")
	}
	filenamePrefix := opts.FilenamePrefix
	if filenamePrefix == "" {
		filenamePrefix = fmt.Sprintf("tts-audio-%d", time.Now().UnixNano())
	}
	if opts.OutputPath != "" {
		resolved, err := validateResolvedOutputPath(opts.BaseDir, opts.OutputPath)
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
			return "", fmt.Errorf("create output directory: %w", err)
		}
		if !opts.Overwrite {
			if _, err := os.Stat(resolved); err == nil {
				return "", fmt.Errorf("output_path already exists: %s", resolved)
			} else if !os.IsNotExist(err) {
				return "", fmt.Errorf("stat output_path: %w", err)
			}
		}
		if err := mediaWriteFileAtomic(resolved, result.Audio, 0o644); err != nil {
			return "", fmt.Errorf("write output file: %w", err)
		}
		return resolved, nil
	}

	dir, err := resolveGeneratedAudioDir(opts.BaseDir, opts.OutputDir)
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
	if !opts.Overwrite {
		if _, err := os.Stat(resolved); err == nil {
			return "", fmt.Errorf("output file already exists: %s", resolved)
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("stat output file: %w", err)
		}
	}
	if err := mediaWriteFileAtomic(resolved, result.Audio, 0o644); err != nil {
		return "", fmt.Errorf("write synthesized audio: %w", err)
	}
	return resolved, nil
}

func plannedSpeechOutputTarget(opts speechSynthesisOptions) (string, error) {
	if opts.OutputPath != "" {
		return validateResolvedOutputPath(opts.BaseDir, opts.OutputPath)
	}
	dir, err := resolveGeneratedAudioDir(opts.BaseDir, opts.OutputDir)
	if err != nil {
		return "", err
	}
	filenamePrefix := opts.FilenamePrefix
	if filenamePrefix == "" {
		filenamePrefix = fmt.Sprintf("tts-audio-%d", time.Now().UnixNano())
	}
	return resolveWorkspacePath(filepath.Join(dir, fmt.Sprintf("%s%s", filenamePrefix, extensionForSpeechFormat(opts.Format))))
}

func validateSpeechOutputConflicts(opts speechSynthesisOptions) error {
	if opts.Overwrite {
		return nil
	}
	target, err := plannedSpeechOutputTarget(opts)
	if err != nil {
		return err
	}
	if _, err := os.Stat(target); err == nil {
		if opts.OutputPath != "" {
			return fmt.Errorf("output_path already exists: %s", target)
		}
		return fmt.Errorf("output file already exists: %s", target)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat output target: %w", err)
	}
	return nil
}

func resolveGeneratedAudioDir(baseDir, outputDir string) (string, error) {
	if outputDir != "" {
		return validateResolvedOutputPath(baseDir, outputDir)
	}
	dir := filepath.Join(sandboxWorkspaceDir(), "generated-audio")
	return resolveWorkspacePath(dir)
}

func speechSpeedArg(args map[string]any, def float64) (float64, error) {
	if def <= 0 {
		def = 1.0
	}
	speed := def
	if raw, ok := args["speed"]; ok {
		switch v := raw.(type) {
		case float64:
			speed = v
		case int:
			speed = float64(v)
		}
	}
	if speed < minTTSSpeed || speed > maxTTSSpeed {
		return 0, fmt.Errorf("speed must be between %.2f and %.1f", minTTSSpeed, maxTTSSpeed)
	}
	return speed, nil
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
	case "opus", "audio/opus":
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

func validateTTSFormat(value string, allowCustom bool) (string, error) {
	format := normalizeTTSFormat(value)
	switch format {
	case "mp3", "wav", "opus", "aac", "flac", "pcm":
		return format, nil
	default:
		if allowCustom && format != "" {
			return format, nil
		}
		return "", fmt.Errorf("unsupported audio format %q; supported formats: mp3, wav, opus, aac, flac, pcm", value)
	}
}

func validateProviderIDString(param, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if len([]rune(value)) > 128 {
		return fmt.Errorf("%s exceeds 128 character limit", param)
	}
	for _, r := range value {
		if r < 32 || r == 127 {
			return fmt.Errorf("%s must not contain control characters", param)
		}
	}
	return nil
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
