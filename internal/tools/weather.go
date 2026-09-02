package tools

import (
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

// httpClient with timeout
var weatherHTTPClient = &http.Client{Timeout: 10 * time.Second}

func executeWeather(_ *auth.ToolIdentity, argumentsInJSON string) ToolResult {
	var args WeatherArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return BusinessErrorResult("weather", "invalid arguments: "+err.Error())
	}

	if args.City == "" {
		return BusinessErrorResult("weather", "city is required")
	}

	// Call wttr.in API — free, no API key needed
	resp, err := queryWttrIn(args.City)
	if err != nil {
		// Fallback to mock data on API failure
		return fallbackMockWeather(args.City, err)
	}

	return formatWeatherResult(args.City, resp)
}

// queryWttrIn calls the wttr.in JSON API.
func queryWttrIn(city string) (*wttrInResponse, error) {
	// URL-encode the city name to handle Chinese characters
	encodedCity := url.PathEscape(city)
	apiURL := fmt.Sprintf("https://wttr.in/%s?format=j1&lang=zh", encodedCity)

	req, err := http.NewRequest("GET", apiURL, nil)
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

// fallbackMockWeather returns mock data when the real API is unavailable.
func fallbackMockWeather(city string, apiErr error) ToolResult {
	// Fallback mock data
	weatherData := map[string]map[string]any{
		"北京": {"temp": "32°C", "condition": "晴", "humidity": "45%"},
		"上海": {"temp": "28°C", "condition": "多云", "humidity": "72%"},
		"深圳": {"temp": "30°C", "condition": "阵雨", "humidity": "85%"},
		"广州": {"temp": "31°C", "condition": "多云", "humidity": "78%"},
		"武汉": {"temp": "35°C", "condition": "晴", "humidity": "60%"},
	}

	data, ok := weatherData[city]
	if !ok {
		data = map[string]any{"temp": "25°C", "condition": "晴", "humidity": "60%"}
	}

	content := fmt.Sprintf("%s天气（离线数据）：%s，温度 %s，湿度 %s\n⚠️ 实时天气API暂不可用: %v", city, data["condition"], data["temp"], data["humidity"], apiErr)
	return SuccessResult("weather", content, map[string]any{
		"city":   city,
		"data":   data,
		"source": "fallback_mock",
	})
}
