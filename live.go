package genai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/gorilla/websocket"
)

// Live struct encapsulates the configuration for realtime interaction with the Generative Language API.
type Live struct {
	apiClient *apiClient
}

// Session struct represents a realtime connection to the API.
type Session struct {
	conn      *websocket.Conn
	apiClient *apiClient
}

// Connect establishes a realtime connection to the specified model with given configuration.
// It returns a Session object representing the connection or an error if the connection fails.
func (r *Live) Connect(model string, config *LiveConnectConfig) (*Session, error) {
	baseURL, err := url.Parse(r.apiClient.clientConfig.baseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse base URL: %w", err)
	}
	scheme := baseURL.Scheme
	// Avoid overwrite schema if websocket scheme is already specified.
	if scheme != "wss" && scheme != "ws" {
		scheme = "wss"
	}

	var u url.URL
	if r.apiClient.clientConfig.Backend == BackendVertexAI {
		return nil, fmt.Errorf("not implemented")
	} else {
		u = url.URL{
			Scheme: scheme,
			Host:   baseURL.Host,
			// Host:     "generativelanguage.googleapis.com",
			// TODO(b/372231289): support custom api version.
			Path:     "/ws/google.ai.generativelanguage.v1alpha.GenerativeService.BidiGenerateContent",
			RawQuery: fmt.Sprintf("key=%s", r.apiClient.clientConfig.APIKey),
		}
	}

	// TODO(b/372730941): support custom header
	header := http.Header{}
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), header)
	if err != nil {
		return nil, fmt.Errorf("Connect to %s failed: %w", u.String(), err)
	}
	s := &Session{
		conn:      conn,
		apiClient: r.apiClient,
	}
	kwargs := map[string]any{"model": model, "config": config}
	parameterMap := make(map[string]any)
	deepMarshal(kwargs, &parameterMap)

	var toConverter func(*apiClient, map[string]any, map[string]any) (map[string]any, error)
	if r.apiClient.clientConfig.Backend == BackendVertexAI {
		toConverter = liveConnectParametersToVertex
	} else {
		toConverter = liveConnectParametersToMldev
	}
	body, err := toConverter(r.apiClient, parameterMap, nil)
	if err != nil {
		return nil, err
	}
	delete(body, "config")

	clientBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal LiveClientSetup failed: %w", err)
	}
	s.conn.WriteMessage(websocket.TextMessage, clientBytes)
	return s, nil
}

// Send transmits a BidiClientMessage over the established websocket connection.
// It returns an error if sending the message fails.
func (s *Session) Send(input *LiveClientMessage) error {
	if input.Setup != nil {
		return fmt.Errorf("message SetUp is not supported in Send(). Use Connect() instead")
	}

	kwargs := map[string]any{"input": input}
	parameterMap := make(map[string]any)
	deepMarshal(kwargs, &parameterMap)

	var toConverter func(*apiClient, map[string]any, map[string]any) (map[string]any, error)
	if s.apiClient.clientConfig.Backend == BackendVertexAI {
		toConverter = liveSendParametersToVertex
	} else {
		toConverter = liveSendParametersToMldev
	}
	body, err := toConverter(s.apiClient, parameterMap, nil)
	if err != nil {
		return err
	}
	delete(body, "input")

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal client message error: %w", err)
	}
	return s.conn.WriteMessage(websocket.TextMessage, []byte(data))
}

// Receive reads a BidiServerMessage from the websocket connection.
// It returns the received message or an error if reading or unmarshalling fails.
func (s *Session) Receive() (*LiveServerMessage, error) {
	messageType, msgBytes, err := s.conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	// TODO(b/365983028): Implement response error handling.
	responseMap := make(map[string]any)
	err = json.Unmarshal(msgBytes, &responseMap)
	if err != nil {
		return nil, fmt.Errorf("invalid message format. messageType: %d, message: %s", messageType, msgBytes)
	}

	var fromConverter func(*apiClient, map[string]any, map[string]any) (map[string]any, error)
	if s.apiClient.clientConfig.Backend == BackendVertexAI {
		fromConverter = liveServerMessageFromVertex
	} else {
		fromConverter = liveServerMessageFromMldev
	}
	responseMap, err = fromConverter(s.apiClient, responseMap, nil)
	if err != nil {
		return nil, err
	}

	var message = new(LiveServerMessage)
	err = mapToStruct(responseMap, message)
	if err != nil {
		return nil, err
	}
	return message, err
}

// Close terminates the websocket connection.
func (s *Session) Close() {
	s.conn.Close()
}

// BEGIN: Converter functions
func liveConnectConfigToMldev(ac *apiClient, fromObject map[string]any, parentObject map[string]any) (toObject map[string]any, err error) {
	toObject = make(map[string]any)

	fromGenerationConfig := getValueByPath(fromObject, []string{"generationConfig"})
	if fromGenerationConfig != nil {
		setValueByPath(parentObject, []string{"setup", "generationConfig"}, fromGenerationConfig)
	}

	fromResponseModalities := getValueByPath(fromObject, []string{"responseModalities"})
	if fromResponseModalities != nil {
		setValueByPath(parentObject, []string{"setup", "generationConfig", "responseModalities"}, fromResponseModalities)
	}

	fromSpeechConfig := getValueByPath(fromObject, []string{"speechConfig"})
	if fromSpeechConfig != nil {
		setValueByPath(parentObject, []string{"setup", "generationConfig", "speechConfig"}, fromSpeechConfig)
	}

	fromSystemInstruction := getValueByPath(fromObject, []string{"systemInstruction"})
	if fromSystemInstruction != nil {
		fromSystemInstruction, err = contentToMldev(ac, fromSystemInstruction.(map[string]any), toObject)
		if err != nil {
			return nil, err
		}

		setValueByPath(parentObject, []string{"setup", "systemInstruction"}, fromSystemInstruction)
	}

	fromTools := getValueByPath(fromObject, []string{"tools"})
	if fromTools != nil {
		fromTools, err = applyConverterToSlice(ac, fromTools.([]any), toolToMldev)
		if err != nil {
			return nil, err
		}

		setValueByPath(parentObject, []string{"setup", "tools"}, fromTools)
	}

	return toObject, nil
}

func liveConnectConfigToVertex(ac *apiClient, fromObject map[string]any, parentObject map[string]any) (toObject map[string]any, err error) {
	toObject = make(map[string]any)

	fromGenerationConfig := getValueByPath(fromObject, []string{"generationConfig"})
	if fromGenerationConfig != nil {
		setValueByPath(parentObject, []string{"setup", "generationConfig"}, fromGenerationConfig)
	}

	fromResponseModalities := getValueByPath(fromObject, []string{"responseModalities"})
	if fromResponseModalities != nil {
		setValueByPath(parentObject, []string{"setup", "generationConfig", "responseModalities"}, fromResponseModalities)
	}

	fromSpeechConfig := getValueByPath(fromObject, []string{"speechConfig"})
	if fromSpeechConfig != nil {
		setValueByPath(parentObject, []string{"setup", "generationConfig", "speechConfig"}, fromSpeechConfig)
	}

	fromSystemInstruction := getValueByPath(fromObject, []string{"systemInstruction"})
	if fromSystemInstruction != nil {
		fromSystemInstruction, err = contentToVertex(ac, fromSystemInstruction.(map[string]any), toObject)
		if err != nil {
			return nil, err
		}

		setValueByPath(parentObject, []string{"setup", "systemInstruction"}, fromSystemInstruction)
	}

	fromTools := getValueByPath(fromObject, []string{"tools"})
	if fromTools != nil {
		fromTools, err = applyConverterToSlice(ac, fromTools.([]any), toolToVertex)
		if err != nil {
			return nil, err
		}

		setValueByPath(parentObject, []string{"setup", "tools"}, fromTools)
	}

	return toObject, nil
}

func liveConnectParametersToMldev(ac *apiClient, fromObject map[string]any, parentObject map[string]any) (toObject map[string]any, err error) {
	toObject = make(map[string]any)

	fromModel := getValueByPath(fromObject, []string{"model"})
	if fromModel != nil {
		setValueByPath(toObject, []string{"setup", "model"}, fromModel)
	}

	fromConfig := getValueByPath(fromObject, []string{"config"})
	if fromConfig != nil {
		fromConfig, err = liveConnectConfigToMldev(ac, fromConfig.(map[string]any), toObject)
		if err != nil {
			return nil, err
		}

		setValueByPath(toObject, []string{"config"}, fromConfig)
	}

	return toObject, nil
}

func liveConnectParametersToVertex(ac *apiClient, fromObject map[string]any, parentObject map[string]any) (toObject map[string]any, err error) {
	toObject = make(map[string]any)

	fromModel := getValueByPath(fromObject, []string{"model"})
	if fromModel != nil {
		setValueByPath(toObject, []string{"setup", "model"}, fromModel)
	}

	fromConfig := getValueByPath(fromObject, []string{"config"})
	if fromConfig != nil {
		fromConfig, err = liveConnectConfigToVertex(ac, fromConfig.(map[string]any), toObject)
		if err != nil {
			return nil, err
		}

		setValueByPath(toObject, []string{"config"}, fromConfig)
	}

	return toObject, nil
}

func liveClientSetupToMldev(ac *apiClient, fromObject map[string]any, parentObject map[string]any) (toObject map[string]any, err error) {
	toObject = make(map[string]any)

	fromModel := getValueByPath(fromObject, []string{"model"})
	if fromModel != nil {
		setValueByPath(toObject, []string{"model"}, fromModel)
	}

	fromGenerationConfig := getValueByPath(fromObject, []string{"generationConfig"})
	if fromGenerationConfig != nil {
		setValueByPath(toObject, []string{"generationConfig"}, fromGenerationConfig)
	}

	fromSystemInstruction := getValueByPath(fromObject, []string{"systemInstruction"})
	if fromSystemInstruction != nil {
		fromSystemInstruction, err = contentToMldev(ac, fromSystemInstruction.(map[string]any), toObject)
		if err != nil {
			return nil, err
		}

		setValueByPath(toObject, []string{"systemInstruction"}, fromSystemInstruction)
	}

	fromTools := getValueByPath(fromObject, []string{"tools"})
	if fromTools != nil {
		fromTools, err = applyConverterToSlice(ac, fromTools.([]any), toolToMldev)
		if err != nil {
			return nil, err
		}

		setValueByPath(toObject, []string{"tools"}, fromTools)
	}

	return toObject, nil
}

func liveClientSetupToVertex(ac *apiClient, fromObject map[string]any, parentObject map[string]any) (toObject map[string]any, err error) {
	toObject = make(map[string]any)

	fromModel := getValueByPath(fromObject, []string{"model"})
	if fromModel != nil {
		setValueByPath(toObject, []string{"model"}, fromModel)
	}

	fromGenerationConfig := getValueByPath(fromObject, []string{"generationConfig"})
	if fromGenerationConfig != nil {
		setValueByPath(toObject, []string{"generationConfig"}, fromGenerationConfig)
	}

	fromSystemInstruction := getValueByPath(fromObject, []string{"systemInstruction"})
	if fromSystemInstruction != nil {
		fromSystemInstruction, err = contentToVertex(ac, fromSystemInstruction.(map[string]any), toObject)
		if err != nil {
			return nil, err
		}

		setValueByPath(toObject, []string{"systemInstruction"}, fromSystemInstruction)
	}

	fromTools := getValueByPath(fromObject, []string{"tools"})
	if fromTools != nil {
		fromTools, err = applyConverterToSlice(ac, fromTools.([]any), toolToVertex)
		if err != nil {
			return nil, err
		}

		setValueByPath(toObject, []string{"tools"}, fromTools)
	}

	return toObject, nil
}

func liveClientContentToMldev(ac *apiClient, fromObject map[string]any, parentObject map[string]any) (toObject map[string]any, err error) {
	toObject = make(map[string]any)

	fromTurns := getValueByPath(fromObject, []string{"turns"})
	if fromTurns != nil {
		fromTurns, err = applyConverterToSlice(ac, fromTurns.([]any), contentToMldev)
		if err != nil {
			return nil, err
		}

		setValueByPath(toObject, []string{"turns"}, fromTurns)
	}

	fromTurnComplete := getValueByPath(fromObject, []string{"turnComplete"})
	if fromTurnComplete != nil {
		setValueByPath(toObject, []string{"turnComplete"}, fromTurnComplete)
	}

	return toObject, nil
}

func liveClientContentToVertex(ac *apiClient, fromObject map[string]any, parentObject map[string]any) (toObject map[string]any, err error) {
	toObject = make(map[string]any)

	fromTurns := getValueByPath(fromObject, []string{"turns"})
	if fromTurns != nil {
		fromTurns, err = applyConverterToSlice(ac, fromTurns.([]any), contentToVertex)
		if err != nil {
			return nil, err
		}

		setValueByPath(toObject, []string{"turns"}, fromTurns)
	}

	fromTurnComplete := getValueByPath(fromObject, []string{"turnComplete"})
	if fromTurnComplete != nil {
		setValueByPath(toObject, []string{"turnComplete"}, fromTurnComplete)
	}

	return toObject, nil
}

func liveClientRealtimeInputToMldev(ac *apiClient, fromObject map[string]any, parentObject map[string]any) (toObject map[string]any, err error) {
	toObject = make(map[string]any)

	fromMediaChunks := getValueByPath(fromObject, []string{"mediaChunks"})
	if fromMediaChunks != nil {
		setValueByPath(toObject, []string{"mediaChunks"}, fromMediaChunks)
	}

	return toObject, nil
}

func liveClientRealtimeInputToVertex(ac *apiClient, fromObject map[string]any, parentObject map[string]any) (toObject map[string]any, err error) {
	toObject = make(map[string]any)

	fromMediaChunks := getValueByPath(fromObject, []string{"mediaChunks"})
	if fromMediaChunks != nil {
		setValueByPath(toObject, []string{"mediaChunks"}, fromMediaChunks)
	}

	return toObject, nil
}

func functionResponseToMldev(ac *apiClient, fromObject map[string]any, parentObject map[string]any) (toObject map[string]any, err error) {
	toObject = make(map[string]any)

	fromId := getValueByPath(fromObject, []string{"id"})
	if fromId != nil {
		setValueByPath(toObject, []string{"id"}, fromId)
	}

	fromName := getValueByPath(fromObject, []string{"name"})
	if fromName != nil {
		setValueByPath(toObject, []string{"name"}, fromName)
	}

	fromResponse := getValueByPath(fromObject, []string{"response"})
	if fromResponse != nil {
		setValueByPath(toObject, []string{"response"}, fromResponse)
	}

	return toObject, nil
}

func functionResponseToVertex(ac *apiClient, fromObject map[string]any, parentObject map[string]any) (toObject map[string]any, err error) {
	toObject = make(map[string]any)
	if getValueByPath(fromObject, []string{"id"}) != nil {
		return nil, fmt.Errorf("id parameter is not supported in Vertex AI")
	}

	fromName := getValueByPath(fromObject, []string{"name"})
	if fromName != nil {
		setValueByPath(toObject, []string{"name"}, fromName)
	}

	fromResponse := getValueByPath(fromObject, []string{"response"})
	if fromResponse != nil {
		setValueByPath(toObject, []string{"response"}, fromResponse)
	}

	return toObject, nil
}

func liveClientToolResponseToMldev(ac *apiClient, fromObject map[string]any, parentObject map[string]any) (toObject map[string]any, err error) {
	toObject = make(map[string]any)

	fromFunctionResponses := getValueByPath(fromObject, []string{"functionResponses"})
	if fromFunctionResponses != nil {
		fromFunctionResponses, err = applyConverterToSlice(ac, fromFunctionResponses.([]any), functionResponseToMldev)
		if err != nil {
			return nil, err
		}

		setValueByPath(toObject, []string{"functionResponses"}, fromFunctionResponses)
	}

	return toObject, nil
}

func liveClientToolResponseToVertex(ac *apiClient, fromObject map[string]any, parentObject map[string]any) (toObject map[string]any, err error) {
	toObject = make(map[string]any)

	fromFunctionResponses := getValueByPath(fromObject, []string{"functionResponses"})
	if fromFunctionResponses != nil {
		fromFunctionResponses, err = applyConverterToSlice(ac, fromFunctionResponses.([]any), functionResponseToVertex)
		if err != nil {
			return nil, err
		}

		setValueByPath(toObject, []string{"functionResponses"}, fromFunctionResponses)
	}

	return toObject, nil
}

func liveClientMessageToMldev(ac *apiClient, fromObject map[string]any, parentObject map[string]any) (toObject map[string]any, err error) {
	toObject = make(map[string]any)

	fromSetup := getValueByPath(fromObject, []string{"setup"})
	if fromSetup != nil {
		fromSetup, err = liveClientSetupToMldev(ac, fromSetup.(map[string]any), toObject)
		if err != nil {
			return nil, err
		}

		setValueByPath(parentObject, []string{"setup"}, fromSetup)
	}

	fromClientContent := getValueByPath(fromObject, []string{"clientContent"})
	if fromClientContent != nil {
		fromClientContent, err = liveClientContentToMldev(ac, fromClientContent.(map[string]any), toObject)
		if err != nil {
			return nil, err
		}

		setValueByPath(parentObject, []string{"clientContent"}, fromClientContent)
	}

	fromRealtimeInput := getValueByPath(fromObject, []string{"realtimeInput"})
	if fromRealtimeInput != nil {
		fromRealtimeInput, err = liveClientRealtimeInputToMldev(ac, fromRealtimeInput.(map[string]any), toObject)
		if err != nil {
			return nil, err
		}

		setValueByPath(parentObject, []string{"realtimeInput"}, fromRealtimeInput)
	}

	fromToolResponse := getValueByPath(fromObject, []string{"toolResponse"})
	if fromToolResponse != nil {
		fromToolResponse, err = liveClientToolResponseToMldev(ac, fromToolResponse.(map[string]any), toObject)
		if err != nil {
			return nil, err
		}

		setValueByPath(parentObject, []string{"toolResponse"}, fromToolResponse)
	}
	return toObject, nil
}

func liveClientMessageToVertex(ac *apiClient, fromObject map[string]any, parentObject map[string]any) (toObject map[string]any, err error) {
	toObject = make(map[string]any)

	fromSetup := getValueByPath(fromObject, []string{"setup"})
	if fromSetup != nil {
		fromSetup, err = liveClientSetupToVertex(ac, fromSetup.(map[string]any), toObject)
		if err != nil {
			return nil, err
		}

		setValueByPath(parentObject, []string{"setup"}, fromSetup)
	}

	fromClientContent := getValueByPath(fromObject, []string{"clientContent"})
	if fromClientContent != nil {
		fromClientContent, err = liveClientContentToVertex(ac, fromClientContent.(map[string]any), toObject)
		if err != nil {
			return nil, err
		}

		setValueByPath(parentObject, []string{"clientContent"}, fromClientContent)
	}

	fromRealtimeInput := getValueByPath(fromObject, []string{"realtimeInput"})
	if fromRealtimeInput != nil {
		fromRealtimeInput, err = liveClientRealtimeInputToVertex(ac, fromRealtimeInput.(map[string]any), toObject)
		if err != nil {
			return nil, err
		}

		setValueByPath(parentObject, []string{"realtimeInput"}, fromRealtimeInput)
	}

	fromToolResponse := getValueByPath(fromObject, []string{"toolResponse"})
	if fromToolResponse != nil {
		fromToolResponse, err = liveClientToolResponseToVertex(ac, fromToolResponse.(map[string]any), toObject)
		if err != nil {
			return nil, err
		}

		setValueByPath(parentObject, []string{"toolResponse"}, fromToolResponse)
	}

	return toObject, nil
}

func liveSendParametersToMldev(ac *apiClient, fromObject map[string]any, parentObject map[string]any) (toObject map[string]any, err error) {
	toObject = make(map[string]any)


	fromInput := getValueByPath(fromObject, []string{"input"})
	if fromInput != nil {
		fromInput, err = liveClientMessageToMldev(ac, fromInput.(map[string]any), toObject)
		if err != nil {
			return nil, err
		}
		setValueByPath(toObject, []string{"input"}, fromInput)
	}
	return toObject, nil
}

func liveSendParametersToVertex(ac *apiClient, fromObject map[string]any, parentObject map[string]any) (toObject map[string]any, err error) {
	toObject = make(map[string]any)

	fromInput := getValueByPath(fromObject, []string{"input"})
	if fromInput != nil {
		fromInput, err = liveClientMessageToVertex(ac, fromInput.(map[string]any), toObject)
		if err != nil {
			return nil, err
		}

		setValueByPath(toObject, []string{"input"}, fromInput)
	}

	return toObject, nil
}


func liveServerSetupCompleteFromMldev(ac *apiClient, fromObject map[string]any, parentObject map[string]any) (toObject map[string]any, err error) {
	toObject = make(map[string]any)

	return toObject, nil
}

func liveServerSetupCompleteFromVertex(ac *apiClient, fromObject map[string]any, parentObject map[string]any) (toObject map[string]any, err error) {
	toObject = make(map[string]any)

	return toObject, nil
}

func liveServerContentFromMldev(ac *apiClient, fromObject map[string]any, parentObject map[string]any) (toObject map[string]any, err error) {
	toObject = make(map[string]any)

	fromModelTurn := getValueByPath(fromObject, []string{"modelTurn"})
	if fromModelTurn != nil {
		fromModelTurn, err = contentFromMldev(ac, fromModelTurn.(map[string]any), toObject)
		if err != nil {
			return nil, err
		}

		setValueByPath(toObject, []string{"modelTurn"}, fromModelTurn)
	}

	fromTurnComplete := getValueByPath(fromObject, []string{"turnComplete"})
	if fromTurnComplete != nil {
		setValueByPath(toObject, []string{"turnComplete"}, fromTurnComplete)
	}

	fromInterrupted := getValueByPath(fromObject, []string{"interrupted"})
	if fromInterrupted != nil {
		setValueByPath(toObject, []string{"interrupted"}, fromInterrupted)
	}

	return toObject, nil
}

func liveServerContentFromVertex(ac *apiClient, fromObject map[string]any, parentObject map[string]any) (toObject map[string]any, err error) {
	toObject = make(map[string]any)

	fromModelTurn := getValueByPath(fromObject, []string{"modelTurn"})
	if fromModelTurn != nil {
		fromModelTurn, err = contentFromVertex(ac, fromModelTurn.(map[string]any), toObject)
		if err != nil {
			return nil, err
		}

		setValueByPath(toObject, []string{"modelTurn"}, fromModelTurn)
	}

	fromTurnComplete := getValueByPath(fromObject, []string{"turnComplete"})
	if fromTurnComplete != nil {
		setValueByPath(toObject, []string{"turnComplete"}, fromTurnComplete)
	}

	fromInterrupted := getValueByPath(fromObject, []string{"interrupted"})
	if fromInterrupted != nil {
		setValueByPath(toObject, []string{"interrupted"}, fromInterrupted)
	}

	return toObject, nil
}

func functionCallFromMldev(ac *apiClient, fromObject map[string]any, parentObject map[string]any) (toObject map[string]any, err error) {
	toObject = make(map[string]any)

	fromId := getValueByPath(fromObject, []string{"id"})
	if fromId != nil {
		setValueByPath(toObject, []string{"id"}, fromId)
	}

	fromArgs := getValueByPath(fromObject, []string{"args"})
	if fromArgs != nil {
		setValueByPath(toObject, []string{"args"}, fromArgs)
	}

	fromName := getValueByPath(fromObject, []string{"name"})
	if fromName != nil {
		setValueByPath(toObject, []string{"name"}, fromName)
	}

	return toObject, nil
}

func functionCallFromVertex(ac *apiClient, fromObject map[string]any, parentObject map[string]any) (toObject map[string]any, err error) {
	toObject = make(map[string]any)

	fromArgs := getValueByPath(fromObject, []string{"args"})
	if fromArgs != nil {
		setValueByPath(toObject, []string{"args"}, fromArgs)
	}

	fromName := getValueByPath(fromObject, []string{"name"})
	if fromName != nil {
		setValueByPath(toObject, []string{"name"}, fromName)
	}

	return toObject, nil
}

func liveServerToolCallFromMldev(ac *apiClient, fromObject map[string]any, parentObject map[string]any) (toObject map[string]any, err error) {
	toObject = make(map[string]any)

	fromFunctionCalls := getValueByPath(fromObject, []string{"functionCalls"})
	if fromFunctionCalls != nil {
		fromFunctionCalls, err = applyConverterToSlice(ac, fromFunctionCalls.([]any), functionCallFromMldev)
		if err != nil {
			return nil, err
		}

		setValueByPath(toObject, []string{"functionCalls"}, fromFunctionCalls)
	}

	return toObject, nil
}

func liveServerToolCallFromVertex(ac *apiClient, fromObject map[string]any, parentObject map[string]any) (toObject map[string]any, err error) {
	toObject = make(map[string]any)

	fromFunctionCalls := getValueByPath(fromObject, []string{"functionCalls"})
	if fromFunctionCalls != nil {
		fromFunctionCalls, err = applyConverterToSlice(ac, fromFunctionCalls.([]any), functionCallFromVertex)
		if err != nil {
			return nil, err
		}

		setValueByPath(toObject, []string{"functionCalls"}, fromFunctionCalls)
	}

	return toObject, nil
}

func liveServerToolCallCancellationFromMldev(ac *apiClient, fromObject map[string]any, parentObject map[string]any) (toObject map[string]any, err error) {
	toObject = make(map[string]any)

	fromIds := getValueByPath(fromObject, []string{"ids"})
	if fromIds != nil {
		setValueByPath(toObject, []string{"ids"}, fromIds)
	}

	return toObject, nil
}

func liveServerToolCallCancellationFromVertex(ac *apiClient, fromObject map[string]any, parentObject map[string]any) (toObject map[string]any, err error) {
	toObject = make(map[string]any)

	fromIds := getValueByPath(fromObject, []string{"ids"})
	if fromIds != nil {
		setValueByPath(toObject, []string{"ids"}, fromIds)
	}

	return toObject, nil
}

func liveServerMessageFromMldev(ac *apiClient, fromObject map[string]any, parentObject map[string]any) (toObject map[string]any, err error) {
	toObject = make(map[string]any)

	fromSetupComplete := getValueByPath(fromObject, []string{"setupComplete"})
	if fromSetupComplete != nil {
		fromSetupComplete, err = liveServerSetupCompleteFromMldev(ac, fromSetupComplete.(map[string]any), toObject)
		if err != nil {
			return nil, err
		}

		setValueByPath(toObject, []string{"setupComplete"}, fromSetupComplete)
	}

	fromServerContent := getValueByPath(fromObject, []string{"serverContent"})
	if fromServerContent != nil {
		fromServerContent, err = liveServerContentFromMldev(ac, fromServerContent.(map[string]any), toObject)
		if err != nil {
			return nil, err
		}

		setValueByPath(toObject, []string{"serverContent"}, fromServerContent)
	}

	fromToolCall := getValueByPath(fromObject, []string{"toolCall"})
	if fromToolCall != nil {
		fromToolCall, err = liveServerToolCallFromMldev(ac, fromToolCall.(map[string]any), toObject)
		if err != nil {
			return nil, err
		}

		setValueByPath(toObject, []string{"toolCall"}, fromToolCall)
	}

	fromToolCallCancellation := getValueByPath(fromObject, []string{"toolCallCancellation"})
	if fromToolCallCancellation != nil {
		fromToolCallCancellation, err = liveServerToolCallCancellationFromMldev(ac, fromToolCallCancellation.(map[string]any), toObject)
		if err != nil {
			return nil, err
		}

		setValueByPath(toObject, []string{"toolCallCancellation"}, fromToolCallCancellation)
	}

	return toObject, nil
}

func liveServerMessageFromVertex(ac *apiClient, fromObject map[string]any, parentObject map[string]any) (toObject map[string]any, err error) {
	toObject = make(map[string]any)

	fromSetupComplete := getValueByPath(fromObject, []string{"setupComplete"})
	if fromSetupComplete != nil {
		fromSetupComplete, err = liveServerSetupCompleteFromVertex(ac, fromSetupComplete.(map[string]any), toObject)
		if err != nil {
			return nil, err
		}

		setValueByPath(toObject, []string{"setupComplete"}, fromSetupComplete)
	}

	fromServerContent := getValueByPath(fromObject, []string{"serverContent"})
	if fromServerContent != nil {
		fromServerContent, err = liveServerContentFromVertex(ac, fromServerContent.(map[string]any), toObject)
		if err != nil {
			return nil, err
		}

		setValueByPath(toObject, []string{"serverContent"}, fromServerContent)
	}

	fromToolCall := getValueByPath(fromObject, []string{"toolCall"})
	if fromToolCall != nil {
		fromToolCall, err = liveServerToolCallFromVertex(ac, fromToolCall.(map[string]any), toObject)
		if err != nil {
			return nil, err
		}

		setValueByPath(toObject, []string{"toolCall"}, fromToolCall)
	}

	fromToolCallCancellation := getValueByPath(fromObject, []string{"toolCallCancellation"})
	if fromToolCallCancellation != nil {
		fromToolCallCancellation, err = liveServerToolCallCancellationFromVertex(ac, fromToolCallCancellation.(map[string]any), toObject)
		if err != nil {
			return nil, err
		}

		setValueByPath(toObject, []string{"toolCallCancellation"}, fromToolCallCancellation)
	}

	return toObject, nil
}

// END: Converter functions
