package openai

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

type nonStreamSSEChoiceState struct {
	role         string
	content      strings.Builder
	reasoning    strings.Builder
	finishReason string
}

func shouldNormalizeNonStreamEventStreamResponse(contentType string, body []byte) bool {
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		return true
	}
	body = bytes.TrimLeft(body, " \t\r\n")
	return bytes.HasPrefix(body, []byte("data:")) ||
		bytes.HasPrefix(body, []byte("event:")) ||
		bytes.HasPrefix(body, []byte("[DONE]"))
}

func normalizeNonStreamEventStreamResponseBody(body []byte) ([]byte, bool, error) {
	payloads, ok := nonStreamEventStreamDataPayloads(body)
	if !ok {
		return nil, false, nil
	}

	response, err := nonStreamEventStreamPayloadsToTextResponse(payloads)
	if err != nil {
		return nil, true, err
	}

	normalizedBody, err := common.Marshal(response)
	if err != nil {
		return nil, true, err
	}
	return normalizedBody, true, nil
}

func nonStreamEventStreamDataPayloads(body []byte) ([]string, bool) {
	lines := strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n")
	payloads := make([]string, 0, len(lines))
	sawEventStream := false

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			sawEventStream = true
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "" || strings.HasPrefix(payload, "[DONE]") {
				continue
			}
			payloads = append(payloads, payload)
			continue
		}
		if line == "[DONE]" ||
			strings.HasPrefix(line, "event:") ||
			strings.HasPrefix(line, "id:") ||
			strings.HasPrefix(line, "retry:") {
			sawEventStream = true
		}
	}

	return payloads, sawEventStream
}

func nonStreamEventStreamPayloadsToTextResponse(payloads []string) (*dto.OpenAITextResponse, error) {
	response := &dto.OpenAITextResponse{
		Object:  "chat.completion",
		Choices: make([]dto.OpenAITextResponseChoice, 0),
	}
	choiceStates := make(map[int]*nonStreamSSEChoiceState)
	choiceOrder := make([]int, 0)

	for _, payload := range payloads {
		if textResponse, ok, err := tryParseNonStreamTextPayload(payload); ok || err != nil {
			return textResponse, err
		}

		var streamResponse dto.ChatCompletionsStreamResponse
		if err := common.Unmarshal(common.StringToByteSlice(payload), &streamResponse); err != nil {
			return nil, fmt.Errorf("invalid event-stream data payload: %w", err)
		}

		if response.Id == "" && streamResponse.Id != "" {
			response.Id = streamResponse.Id
		}
		if streamResponse.Object != "" {
			response.Object = strings.TrimSuffix(streamResponse.Object, ".chunk")
		}
		if response.Created == nil || streamResponse.Created != 0 {
			response.Created = streamResponse.Created
		}
		if streamResponse.Model != "" {
			response.Model = streamResponse.Model
		}
		if streamResponse.Usage != nil {
			response.Usage = *streamResponse.Usage
		}

		for _, choice := range streamResponse.Choices {
			state, ok := choiceStates[choice.Index]
			if !ok {
				state = &nonStreamSSEChoiceState{}
				choiceStates[choice.Index] = state
				choiceOrder = append(choiceOrder, choice.Index)
			}
			if choice.Delta.Role != "" {
				state.role = choice.Delta.Role
			}
			state.content.WriteString(choice.Delta.GetContentString())
			state.reasoning.WriteString(choice.Delta.GetReasoningContent())
			if choice.FinishReason != nil {
				state.finishReason = *choice.FinishReason
			}
		}
	}

	for _, index := range choiceOrder {
		state := choiceStates[index]
		choice := dto.OpenAITextResponseChoice{
			Index:        index,
			FinishReason: state.finishReason,
		}
		if state.role == "" {
			state.role = "assistant"
		}
		choice.Message.Role = state.role
		choice.Message.SetStringContent(state.content.String())
		if reasoning := state.reasoning.String(); reasoning != "" {
			choice.Message.ReasoningContent = &reasoning
		}
		response.Choices = append(response.Choices, choice)
	}

	return response, nil
}

func tryParseNonStreamTextPayload(payload string) (*dto.OpenAITextResponse, bool, error) {
	var textResponse dto.OpenAITextResponse
	if err := common.Unmarshal(common.StringToByteSlice(payload), &textResponse); err != nil {
		return nil, false, nil
	}

	if oaiError := textResponse.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		if textResponse.Choices == nil {
			textResponse.Choices = make([]dto.OpenAITextResponseChoice, 0)
		}
		return &textResponse, true, nil
	}

	if textResponse.Object == "" || strings.HasSuffix(textResponse.Object, ".chunk") {
		return nil, false, nil
	}

	if textResponse.Choices == nil {
		textResponse.Choices = make([]dto.OpenAITextResponseChoice, 0)
	}
	return &textResponse, true, nil
}
