package tool

import (
	"fmt"

	"github.com/yurika0211/luckyagent/internal/multimodal"
)

// ImageReadTool returns image content to the current model without invoking
// another model. The agent exposes it only on turns using primary vision.
func ImageReadTool() *Tool {
	t := ImageAnalyzeTool(nil, "")
	t.Name = "image_read"
	t.Description = "Load an image for you to inspect directly. Accepts one of path, url, or base64_data. No external image analysis is performed."
	delete(t.Parameters, "provider")
	delete(t.Parameters, "format")
	t.Handler = func(map[string]any) (string, error) {
		return "", fmt.Errorf("image_read requires structured tool result support")
	}
	t.DetailedHandler = func(args map[string]any) (ToolCallResult, error) {
		opts, err := parseImageAnalyzeOptions(args)
		if err != nil {
			return ToolCallResult{}, err
		}
		input, err := buildImageAnalyzeInput(opts)
		if err != nil {
			return ToolCallResult{}, err
		}
		if input.Modality != multimodal.ModalityImage {
			return ToolCallResult{}, fmt.Errorf("image_read requires an image; use document_read for documents")
		}
		return ToolCallResult{Output: "Image loaded. Inspect the image attached to this tool result.", Observations: []Observation{{
			Kind: "image", FilePath: input.FilePath, ImageURL: input.URL, ImageData: input.Data, MimeType: input.MimeType,
		}}}, nil
	}
	return t
}
