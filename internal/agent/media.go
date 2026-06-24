package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/yurika0211/luckyharness/internal/gateway"
	"github.com/yurika0211/luckyharness/internal/multimodal"
	"github.com/yurika0211/luckyharness/internal/tool"
	"github.com/yurika0211/luckyharness/internal/utils"
)

/*
AnalyzeAttachments 使用多模态处理器分析附件并返回汇总文本。
*/
func (a *Agent) AnalyzeAttachments(ctx context.Context, attachments []gateway.Attachment) (string, error) {
	if a == nil || a.mediaProcessor == nil || len(attachments) == 0 {
		return "", nil
	}

	var sections []string
	for i, att := range attachments {
		if section, ok := analyzeDocumentAttachment(att, i); ok {
			sections = append(sections, section)
			continue
		}
		input, title, err := buildMultimodalInput(att)
		if err != nil {
			sections = append(sections, fmt.Sprintf("%s\n- error: %s", attachmentTitle(att, i), err.Error()))
			continue
		}

		result, err := a.analyzeMultimodalInput(ctx, input)
		if err != nil {
			sections = append(sections, fmt.Sprintf("%s\n- error: %s", title, err.Error()))
			continue
		}

		sections = append(sections, formatAttachmentAnalysis(title, result))
	}

	if len(sections) == 0 {
		return "", nil
	}

	return "[Multimodal Analysis]\n" + strings.Join(sections, "\n\n"), nil
}

func analyzeDocumentAttachment(att gateway.Attachment, idx int) (string, bool) {
	if att.Type != gateway.AttachmentDocument || strings.TrimSpace(att.FilePath) == "" {
		return "", false
	}
	ext := strings.ToLower(filepath.Ext(att.FilePath))
	if ext == "" {
		ext = strings.ToLower(filepath.Ext(att.FileName))
	}
	switch ext {
	case ".pdf", ".docx", ".pptx":
	default:
		return "", false
	}
	text, format, err := tool.ExtractDocumentText(att.FilePath)
	title := attachmentTitle(att, idx)
	if err != nil {
		return fmt.Sprintf("%s\n- error: %s", title, err.Error()), true
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Sprintf("%s\n- format: %s\n- extracted: no text found", title, format), true
	}
	return fmt.Sprintf("%s\n- format: %s\n- extracted:\n%s", title, format, utils.Truncate(text, 4000)), true
}

func (a *Agent) analyzeMultimodalInput(ctx context.Context, input *multimodal.Input) (*multimodal.AnalysisResult, error) {
	if a == nil || a.mediaProcessor == nil {
		return nil, fmt.Errorf("multimodal processor is not configured")
	}
	if input == nil {
		return nil, fmt.Errorf("multimodal input is nil")
	}

	if providerName := a.preferredMultimodalProvider(input.Modality); providerName != "" {
		return a.mediaProcessor.AnalyzeWithProvider(ctx, providerName, input)
	}
	return a.mediaProcessor.Analyze(ctx, input)
}

func (a *Agent) preferredMultimodalProvider(modality multimodal.Modality) string {
	if a == nil || a.cfg == nil {
		return ""
	}
	cfg := a.cfg.Get()
	switch modality {
	case multimodal.ModalityImage:
		return strings.TrimSpace(cfg.Multimodal.ImageProvider)
	default:
		return ""
	}
}

/*
buildMultimodalInput 将网关附件转换为多模态分析输入。
*/
func buildMultimodalInput(att gateway.Attachment) (*multimodal.Input, string, error) {
	modality := attachmentModality(att)
	title := attachmentTitle(att, 0)

	if modality == multimodal.ModalityText {
		return nil, title, fmt.Errorf("unsupported attachment type %q", att.Type)
	}

	if len(att.Data) > 0 {
		input := multimodal.NewInput(modality, att.MimeType, att.Data)
		if input.Metadata == nil {
			input.Metadata = make(map[string]string)
		}
		input.Metadata["filename"] = att.FileName
		input.Metadata["file_url"] = att.FileURL
		return input, title, nil
	}

	if strings.TrimSpace(att.FilePath) != "" {
		input := multimodal.NewInputFromPath(modality, att.FilePath)
		input.MimeType = att.MimeType
		input.Metadata = map[string]string{
			"filename":  att.FileName,
			"file_url":  att.FileURL,
			"file_path": att.FilePath,
		}
		return input, title, nil
	}

	if strings.TrimSpace(att.FileURL) != "" {
		input := multimodal.NewInputFromURL(modality, att.FileURL)
		input.MimeType = att.MimeType
		input.Metadata = map[string]string{
			"filename":  att.FileName,
			"file_url":  att.FileURL,
			"file_path": att.FilePath,
		}
		return input, title, nil
	}

	return nil, title, fmt.Errorf("attachment has no downloaded file, data, or url")
}

/*
attachmentModality 根据附件类型推断多模态处理所需的模态。
*/
func attachmentModality(att gateway.Attachment) multimodal.Modality {
	switch att.Type {
	case gateway.AttachmentImage:
		return multimodal.ModalityImage
	case gateway.AttachmentAudio:
		return multimodal.ModalityAudio
	case gateway.AttachmentVideo:
		return multimodal.ModalityVideo
	case gateway.AttachmentDocument:
		if strings.EqualFold(strings.TrimSpace(att.MimeType), "application/pdf") || strings.EqualFold(filepath.Ext(att.FileName), ".pdf") {
			return multimodal.ModalityDocument
		}
		return multimodal.ModalityDocument
	default:
		return multimodal.ModalityText
	}
}

/*
attachmentTitle 生成人类可读的附件标题。
*/
func attachmentTitle(att gateway.Attachment, idx int) string {
	name := strings.TrimSpace(att.FileName)
	if name == "" {
		name = "unnamed"
	}

	prefix := "Attachment"
	switch att.Type {
	case gateway.AttachmentImage:
		prefix = "Image"
	case gateway.AttachmentAudio:
		prefix = "Audio"
	case gateway.AttachmentVideo:
		prefix = "Video"
	case gateway.AttachmentDocument:
		prefix = "Document"
	}

	if idx > 0 {
		return fmt.Sprintf("%s %d: %s", prefix, idx, name)
	}
	return fmt.Sprintf("%s: %s", prefix, name)
}

/*
formatAttachmentAnalysis 将单个附件的分析结果格式化为文本。
*/
func formatAttachmentAnalysis(title string, result *multimodal.AnalysisResult) string {
	if result == nil {
		return title + "\n- analysis: unavailable"
	}

	lines := []string{title}
	if summary := strings.TrimSpace(result.Summary); summary != "" {
		lines = append(lines, "- summary: "+utils.Truncate(summary, 400))
	}
	if text := strings.TrimSpace(result.Text); text != "" {
		lines = append(lines, "- extracted: "+utils.Truncate(text, 1200))
	}
	if len(result.Labels) > 0 {
		lines = append(lines, "- labels: "+strings.Join(result.Labels, ", "))
	}
	if result.Confidence > 0 {
		lines = append(lines, fmt.Sprintf("- confidence: %.2f", result.Confidence))
	}
	if result.Metadata != nil {
		if model := strings.TrimSpace(result.Metadata["model"]); model != "" {
			lines = append(lines, "- model: "+model)
		}
		if source := strings.TrimSpace(result.Metadata["source"]); source != "" {
			lines = append(lines, "- source: "+source)
		}
	}
	return strings.Join(lines, "\n")
}
