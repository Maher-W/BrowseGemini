package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Maher-W/BrowseGemini/server/internal/hub"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ServerWrapper struct {
	mcpServer *mcp.Server
	hub       *hub.Hub
}

func NewServer(h *hub.Hub) *ServerWrapper {
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "browse-gemini",
		Version: "1.0.0",
	}, nil)

	sw := &ServerWrapper{
		mcpServer: s,
		hub:       h,
	}

	sw.registerTools()
	return sw
}

func (sw *ServerWrapper) Handler() http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return sw.mcpServer
	}, &mcp.StreamableHTTPOptions{})
}

func (sw *ServerWrapper) ensureConnected() error {
	if !sw.hub.IsConnected() {
		return fmt.Errorf("Chrome extension is currently toggled OFF. Ask the user to enable it in their browser.")
	}
	return nil
}

// -------------------------------------------------------------
// Input Structs with jsonschema tags for Gemini
// -------------------------------------------------------------

type BrowserTabsInput struct {
	Action string  `json:"action" jsonschema:"Action to perform: 'list' (list all tabs), 'new' (open new tab), 'switch' (focus tab), 'close' (close tab)"`
	TabID  *int    `json:"tabId,omitempty" jsonschema:"Target tab ID (required for 'switch' and 'close')"`
	URL    *string `json:"url,omitempty" jsonschema:"URL to navigate to when opening a 'new' tab (defaults to 'about:blank')"`
}

type BrowserScreenshotInput struct {
	TabID *int `json:"tabId,omitempty" jsonschema:"Target tab ID to capture (defaults to the currently active tab)"`
}

type BrowserGetElementsInput struct {
	TabID *int `json:"tabId,omitempty" jsonschema:"Target tab ID to inspect (defaults to the currently active tab)"`
}

type BrowserClickInput struct {
	X          int     `json:"x" jsonschema:"Viewport X coordinate to click"`
	Y          int     `json:"y" jsonschema:"Viewport Y coordinate to click"`
	Button     *string `json:"button,omitempty" jsonschema:"Mouse button: 'left' (default), 'right', or 'middle'"`
	ClickCount *int    `json:"clickCount,omitempty" jsonschema:"Click count: 1 for single click (default), 2 for double click"`
	TabID      *int    `json:"tabId,omitempty" jsonschema:"Target tab ID (defaults to active tab)"`
}

type BrowserMoveMouseInput struct {
	X     int  `json:"x" jsonschema:"Viewport X coordinate to move cursor to"`
	Y     int  `json:"y" jsonschema:"Viewport Y coordinate to move cursor to"`
	TabID *int `json:"tabId,omitempty" jsonschema:"Target tab ID (defaults to active tab)"`
}

type BrowserDragInput struct {
	StartX int  `json:"startX" jsonschema:"Starting X coordinate"`
	StartY int  `json:"startY" jsonschema:"Starting Y coordinate"`
	EndX   int  `json:"endX" jsonschema:"Ending X coordinate"`
	EndY   int  `json:"endY" jsonschema:"Ending Y coordinate"`
	TabID  *int `json:"tabId,omitempty" jsonschema:"Target tab ID (defaults to active tab)"`
}

type BrowserScrollInput struct {
	X      *int `json:"x,omitempty" jsonschema:"Viewport X coordinate to center scroll on (default 640)"`
	Y      *int `json:"y,omitempty" jsonschema:"Viewport Y coordinate to center scroll on (default 360)"`
	DeltaX *int `json:"deltaX,omitempty" jsonschema:"Horizontal scroll pixels (positive = right, negative = left)"`
	DeltaY *int `json:"deltaY,omitempty" jsonschema:"Vertical scroll pixels (positive = down, negative = up)"`
	TabID  *int `json:"tabId,omitempty" jsonschema:"Target tab ID (defaults to active tab)"`
}

type BrowserTypeInput struct {
	Text       string `json:"text" jsonschema:"Text string to insert into the currently focused input element"`
	PressEnter *bool  `json:"pressEnter,omitempty" jsonschema:"Whether to automatically press the Enter key after typing"`
	TabID      *int   `json:"tabId,omitempty" jsonschema:"Target tab ID (defaults to active tab)"`
}

type BrowserShortcutInput struct {
	Shortcut string `json:"shortcut" jsonschema:"Shortcut key combo to press (e.g. 'Ctrl+C', 'Ctrl+V', 'Ctrl+X', 'Ctrl+A', 'Ctrl+Z', 'Ctrl+T', 'Ctrl+W', 'Ctrl+Shift+T', 'Ctrl+Tab', 'Ctrl+R', 'Alt+Left', 'Alt+Right', 'Enter', 'Escape', 'Tab', 'Backspace', 'ArrowDown', 'ArrowUp')"`
	TabID    *int   `json:"tabId,omitempty" jsonschema:"Target tab ID (defaults to active tab)"`
}

type BrowserNavigateInput struct {
	URL   string `json:"url" jsonschema:"Target URL to load, or navigation keywords 'back', 'forward', 'reload'"`
	TabID *int   `json:"tabId,omitempty" jsonschema:"Target tab ID (defaults to active tab)"`
}

// -------------------------------------------------------------
// Tool Registration
// -------------------------------------------------------------

func (sw *ServerWrapper) registerTools() {
	// 1. browser_tabs
	mcp.AddTool(sw.mcpServer, &mcp.Tool{
		Name:        "browser_tabs",
		Description: "Manage browser tabs: list all open tabs with their IDs, open a new tab, switch active tab focus, or close a tab.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in BrowserTabsInput) (*mcp.CallToolResult, any, error) {
		if err := sw.ensureConnected(); err != nil {
			return errorResult(err), nil, nil
		}
		resp, err := sw.hub.SendCommand(ctx, "browser_tabs", in)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(string(resp.Data)), nil, nil
	})

	// 2. browser_screenshot
	mcp.AddTool(sw.mcpServer, &mcp.Tool{
		Name:        "browser_screenshot",
		Description: "Captures a high-resolution screenshot of the active (or specified) browser tab via CDP Page.captureScreenshot (JPEG max width 1280px), returning both the visual image and viewport metadata.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in BrowserScreenshotInput) (*mcp.CallToolResult, any, error) {
		if err := sw.ensureConnected(); err != nil {
			return errorResult(err), nil, nil
		}
		resp, err := sw.hub.SendCommand(ctx, "browser_screenshot", in)
		if err != nil {
			return errorResult(err), nil, nil
		}

		var payload struct {
			TabID  int    `json:"tabId"`
			URL    string `json:"url"`
			Title  string `json:"title"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
			Image  string `json:"image"`
		}
		if err := json.Unmarshal(resp.Data, &payload); err != nil {
			return errorResult(err), nil, nil
		}

		desc := fmt.Sprintf("Tab #%d (%dx%d px)\nTitle: %s\nURL: %s",
			payload.TabID, payload.Width, payload.Height, payload.Title, payload.URL)

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.ImageContent{
					Data:     []byte(payload.Image),
					MIMEType: "image/jpeg",
				},
				&mcp.TextContent{
					Text: desc,
				},
			},
		}, nil, nil
	})

	// 3. browser_get_elements
	mcp.AddTool(sw.mcpServer, &mcp.Tool{
		Name:        "browser_get_elements",
		Description: "Inspects the page and returns all visible interactive elements (links, buttons, inputs, dropdowns) with their center (x, y) coordinates, text/aria-labels, tag, and index.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in BrowserGetElementsInput) (*mcp.CallToolResult, any, error) {
		if err := sw.ensureConnected(); err != nil {
			return errorResult(err), nil, nil
		}
		resp, err := sw.hub.SendCommand(ctx, "browser_get_elements", in)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(string(resp.Data)), nil, nil
	})

	// 4. browser_click
	mcp.AddTool(sw.mcpServer, &mcp.Tool{
		Name:        "browser_click",
		Description: "Dispatches trusted CDP mouse events (moved, pressed, released) at the specified viewport (x, y) coordinate. Supports left/right/middle button and single/double clicks.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in BrowserClickInput) (*mcp.CallToolResult, any, error) {
		if err := sw.ensureConnected(); err != nil {
			return errorResult(err), nil, nil
		}
		resp, err := sw.hub.SendCommand(ctx, "browser_click", in)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(string(resp.Data)), nil, nil
	})

	// 5. browser_move_mouse
	mcp.AddTool(sw.mcpServer, &mcp.Tool{
		Name:        "browser_move_mouse",
		Description: "Moves the mouse cursor to the specified viewport (x, y) coordinate via CDP Input.dispatchMouseEvent.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in BrowserMoveMouseInput) (*mcp.CallToolResult, any, error) {
		if err := sw.ensureConnected(); err != nil {
			return errorResult(err), nil, nil
		}
		resp, err := sw.hub.SendCommand(ctx, "browser_move_mouse", in)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(string(resp.Data)), nil, nil
	})

	// 6. browser_drag
	mcp.AddTool(sw.mcpServer, &mcp.Tool{
		Name:        "browser_drag",
		Description: "Drags from (startX, startY) to (endX, endY) with intermediate mouse movements to simulate a natural drag-and-drop gesture.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in BrowserDragInput) (*mcp.CallToolResult, any, error) {
		if err := sw.ensureConnected(); err != nil {
			return errorResult(err), nil, nil
		}
		resp, err := sw.hub.SendCommand(ctx, "browser_drag", in)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(string(resp.Data)), nil, nil
	})

	// 7. browser_scroll
	mcp.AddTool(sw.mcpServer, &mcp.Tool{
		Name:        "browser_scroll",
		Description: "Scrolls the page or container at (x, y) by deltaX and deltaY pixels using trusted CDP mouseWheel events.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in BrowserScrollInput) (*mcp.CallToolResult, any, error) {
		if err := sw.ensureConnected(); err != nil {
			return errorResult(err), nil, nil
		}
		resp, err := sw.hub.SendCommand(ctx, "browser_scroll", in)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(string(resp.Data)), nil, nil
	})

	// 8. browser_type
	mcp.AddTool(sw.mcpServer, &mcp.Tool{
		Name:        "browser_type",
		Description: "Types text directly into the focused web element via CDP Input.insertText, with optional Enter key press.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in BrowserTypeInput) (*mcp.CallToolResult, any, error) {
		if err := sw.ensureConnected(); err != nil {
			return errorResult(err), nil, nil
		}
		resp, err := sw.hub.SendCommand(ctx, "browser_type", in)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(string(resp.Data)), nil, nil
	})

	// 9. browser_shortcut
	mcp.AddTool(sw.mcpServer, &mcp.Tool{
		Name:        "browser_shortcut",
		Description: "Executes keyboard shortcuts and keys. Browser-level shortcuts (Ctrl+T, Ctrl+W, Ctrl+Shift+T, Ctrl+Tab, Ctrl+R, Alt+Left, Alt+Right) are intercepted and executed natively via Chrome APIs; page-level shortcuts (Ctrl+C, Ctrl+V, Ctrl+A, Escape, Enter, Arrow keys) are dispatched via CDP Input.dispatchKeyEvent.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in BrowserShortcutInput) (*mcp.CallToolResult, any, error) {
		if err := sw.ensureConnected(); err != nil {
			return errorResult(err), nil, nil
		}
		resp, err := sw.hub.SendCommand(ctx, "browser_shortcut", in)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(string(resp.Data)), nil, nil
	})

	// 10. browser_navigate
	mcp.AddTool(sw.mcpServer, &mcp.Tool{
		Name:        "browser_navigate",
		Description: "Navigates the tab to a specific URL, or executes navigation actions: 'back', 'forward', 'reload'.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in BrowserNavigateInput) (*mcp.CallToolResult, any, error) {
		if err := sw.ensureConnected(); err != nil {
			return errorResult(err), nil, nil
		}
		resp, err := sw.hub.SendCommand(ctx, "browser_navigate", in)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(string(resp.Data)), nil, nil
	})
}

func errorResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: err.Error(),
			},
		},
	}
}

func textResult(content string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: content,
			},
		},
	}
}
