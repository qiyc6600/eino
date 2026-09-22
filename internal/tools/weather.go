package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/example/agent-eino-demo/internal/auth"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// WeatherArgs represents arguments for the weather tool.
type WeatherArgs struct {
	City string `json:"city"`
}

// wttrInResponse is the response from wttr.in API.
type wttrInResponse struct {
	CurrentCondition []struct {
		TempC       string `json:"temp_C"`
		FeelsLikeC  string `json:"FeelsLikeC"`
		Humidity    string `json:"humidity"`
		WeatherDesc []struct {
			Value string `json:"value"`
		} `json:"weatherDesc"`
		Winddir16Point  string `json:"winddir16Point"`
		WindspeedKmph   string `json:"windspeedKmph"`
		ObservationTime string `json:"observation_time"`
	} `json:"current_condition"`
	NearestArea []struct {
		AreaName []struct {
			Value string `json:"value"`
		} `json:"areaName"`
		Country []struct {
			Value string `json:"value"`
		} `json:"country"`
	} `json:"nearest_area"`
}

// NewWeatherTool creates the weather tool with real API call.
func NewWeatherTool() RegisteredTool {
	return RegisteredTool{
		Meta: ToolMeta{
			Name:             "weather",
			Description:      "Query the current weather for a given city. Calls real weather API (wttr.in).",
			RequiredPerm:     "weather",
			RiskLevel:        RiskLevelLow,
			RequiresApproval: false,
			ParamSchema: `{
				"type": "object",
				"properties": {
					"city": {"type": "string", "description": "城市名称，如：北京、上海、武汉、Tokyo、London"}
				},
				"required": ["city"]
			}`,
		},
		Fn: executeWeather,
	}
}

// weatherHTTPClient is package-level so tests can substitute a stub transport;
// without that seam the tool is untestable offline.
var weatherHTTPClient = &http.Client{Timeout: 10 * time.Second}

func executeWeather(identity *auth.ToolIdentity, argumentsInJSON string) ToolResult {
	var args WeatherArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return BusinessErrorResult("weather", "invalid arguments: "+err.Error())
	}

	if args.City == "" {
		return BusinessErrorResult("weather", "city is required")
	}

	// Call wttr.in API — free, no API key needed
	ctx := context.Background()
	if identity != nil && identity.Context != nil {
		ctx = identity.Context
	}
	resp, err := queryWttrInContext(ctx, args.City)
	if err != nil {
		if ctx.Err() != nil {
			return SystemErrorResult("weather", ctx.Err().Error(), "")
		}
		// Report the failure rather than substituting invented numbers. This tool's
		// entire value is "current, real-world conditions", so there is no honest
		// degradation available: a fabricated 32°C returned as a success would be
		// indistinguishable from real data to anything that branches on status, and
		// the model would relay it as an observation.
		return BusinessErrorResult("weather",
			fmt.Sprintf("weather lookup failed for %s: %v", args.City, err))
	}

	return formatWeatherResult(args.City, resp)
}

// queryWttrInContext calls the wttr.in JSON API.
//
// lang=zh is not requested: wttr.in's translated fields (lang_zh) come back
// untranslated in practice — verified across six cities — and the description
// below is read from weatherDesc, which is English-only either way. Asking for a
// translation that is never read would only mislead the next reader.
func queryWttrInContext(ctx context.Context, city string) (*wttrInResponse, error) {
	// URL-encode the city name to handle Chinese characters
	encodedCity := url.PathEscape(city)
	apiURL := fmt.Sprintf("https://wttr.in/%s?format=j1", encodedCity)

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	// Set User-Agent — wttr.in requires it
	req.Header.Set("User-Agent", "agent-eino-demo/1.0")

	resp, err := weatherHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var result wttrInResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(result.CurrentCondition) == 0 {
		return nil, fmt.Errorf("no weather data returned for city: %s", city)
	}

	return &result, nil
}

// formatWeatherResult formats the API response into a user-friendly string.
func formatWeatherResult(city string, resp *wttrInResponse) ToolResult {
	cond := resp.CurrentCondition[0]

	// Get area name from nearest area
	areaName := city
	countryName := ""
	if len(resp.NearestArea) > 0 {
		if len(resp.NearestArea[0].AreaName) > 0 {
			areaName = resp.NearestArea[0].AreaName[0].Value
		}
		if len(resp.NearestArea[0].Country) > 0 {
			countryName = resp.NearestArea[0].Country[0].Value
		}
	}

	// Weather description
	desc := "未知"
	if len(cond.WeatherDesc) > 0 {
		desc = cond.WeatherDesc[0].Value
	}

	// Build content
	var content strings.Builder
	content.WriteString(fmt.Sprintf("🌍 %s", areaName))
	if countryName != "" {
		content.WriteString(fmt.Sprintf("，%s", countryName))
	}
	content.WriteString("\n")
	content.WriteString(fmt.Sprintf("🌡️ 温度：%s°C（体感 %s°C）\n", cond.TempC, cond.FeelsLikeC))
	content.WriteString(fmt.Sprintf("🌤️ 天气：%s\n", desc))
	content.WriteString(fmt.Sprintf("💧 湿度：%s%%\n", cond.Humidity))
	content.WriteString(fmt.Sprintf("💨 风向：%s，风速 %s km/h\n", cond.Winddir16Point, cond.WindspeedKmph))
	content.WriteString(fmt.Sprintf("🕐 观测时间：%s", cond.ObservationTime))

	return SuccessResult("weather", content.String(), map[string]any{
		"city":       areaName,
		"country":    countryName,
		"temp_c":     cond.TempC,
		"feels_like": cond.FeelsLikeC,
		"humidity":   cond.Humidity,
		"weather":    desc,
		"wind_dir":   cond.Winddir16Point,
		"wind_speed": cond.WindspeedKmph,
		"source":     "wttr.in",
	})
}
