package httpapi

import (
	"net/http"
	"sort"

	"github.com/el-amin-dev/barqr/internal/builder"
	"github.com/el-amin-dev/barqr/internal/encoder"
	"github.com/el-amin-dev/barqr/internal/render"
	"github.com/el-amin-dev/barqr/internal/version"
	"github.com/el-amin-dev/barqr/internal/writer"
)

// handleOpenAPI serves the API description.
//
// The document is generated from the same registries and struct tags the
// server actually uses, so it cannot drift from the implementation: a new
// output format appears in the enum the moment its writer registers, and a new
// request field appears the moment it is declared on Request.
func (s *Server) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	s.cacheable(w)
	s.writeJSON(w, r, http.StatusOK, s.openAPIDocument())
}

// openAPIDocument builds the OpenAPI 3.1 description.
func (s *Server) openAPIDocument() map[string]any {
	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       "barqr",
			"summary":     "QR codes and barcodes over HTTP.",
			"description": "One stateless container. HTTP in, image out.",
			"version":     version.Version,
			"license":     map[string]any{"name": "Apache-2.0", "identifier": "Apache-2.0"},
		},
		"servers":    []any{map[string]any{"url": "/", "description": "this instance"}},
		"security":   s.securityRequirement(),
		"paths":      s.openAPIPaths(),
		"components": s.openAPIComponents(),
	}
}

// securityRequirement reflects the running configuration rather than a fixed
// assumption, so a generated client matches the instance it was generated from.
func (s *Server) securityRequirement() []any {
	if s.cfg.APIKeyCount() == 0 && s.cfg.AuthMode != "required" {
		return []any{}
	}
	return []any{
		map[string]any{"apiKey": []any{}},
		map[string]any{"bearer": []any{}},
	}
}

// openAPIPaths describes every route.
func (s *Server) openAPIPaths() map[string]any {
	imageResponses := map[string]any{
		"200": map[string]any{
			"description": "the rendered code",
			"headers": map[string]any{
				"ETag": map[string]any{
					"description": "strong validator over the response body",
					"schema":      map[string]any{"type": "string"},
				},
			},
			"content": s.outputContentTypes(),
		},
		"304": map[string]any{"description": "the code is unchanged since the supplied ETag"},
		"400": errorResponse("the request could not be understood"),
		"401": errorResponse("a valid API key is required"),
		"429": errorResponse("rate limit exceeded"),
	}

	return map[string]any{
		"/v1/healthz": map[string]any{"get": simpleOp(
			"liveness", "Answers 200 while the process can serve, including while draining.")},
		"/v1/readyz": map[string]any{"get": simpleOp(
			"readiness", "Answers 503 as soon as shutdown begins, so load balancers drain.")},
		"/v1/version": map[string]any{"get": simpleOp(
			"build identity", "Version, commit, build date, Go version, and platform.")},
		"/v1/symbologies": map[string]any{"get": simpleOp(
			"capabilities", "What this build can encode, render, and write.")},
		"/v1/openapi.json": map[string]any{"get": simpleOp(
			"this document", "Generated from the live registries.")},

		"/v1/qr": map[string]any{
			"get": map[string]any{
				"summary":     "Render a QR code",
				"description": "Every option is available as a dot-notation query parameter.",
				"parameters":  queryParameters(),
				"responses":   imageResponses,
			},
			"post": map[string]any{
				"summary":     "Render a QR code",
				"description": "Accepts JSON, multipart, or form-encoded bodies.",
				"requestBody": requestBodyRef(),
				"responses":   imageResponses,
			},
		},

		"/v1/barcode/{symbology}": map[string]any{
			"get": map[string]any{
				"summary":    "Render any registered symbology",
				"parameters": append(symbologyPathParam(), queryParameters()...),
				"responses":  imageResponses,
			},
			"post": map[string]any{
				"summary":     "Render any registered symbology",
				"parameters":  symbologyPathParam(),
				"requestBody": requestBodyRef(),
				"responses":   imageResponses,
			},
		},

		"/v1/build/{type}": map[string]any{
			"get": map[string]any{
				"summary":    "Build a payload string without rendering",
				"parameters": append(builderPathParam(), queryParameters()...),
				"responses": map[string]any{
					"200": map[string]any{"description": "the raw string that would be encoded"},
					"400": errorResponse("the payload was rejected by the builder"),
				},
			},
			"post": map[string]any{
				"summary":     "Build a payload string without rendering",
				"parameters":  builderPathParam(),
				"requestBody": requestBodyRef(),
				"responses": map[string]any{
					"200": map[string]any{"description": "the raw string that would be encoded"},
					"400": errorResponse("the payload was rejected by the builder"),
				},
			},
		},

		"/v1/validate": map[string]any{
			"post": map[string]any{
				"summary": "Diagnose a request without returning an image",
				"description": "Runs build, encode, and render, then reports the " +
					"scannability verdict. Cheap enough to run over a whole catalogue.",
				"requestBody": requestBodyRef(),
				"responses": map[string]any{
					"200": map[string]any{"description": "the diagnostic report"},
					"400": errorResponse("the request could not be rendered"),
				},
			},
		},

		"/v1/decode": map[string]any{
			"post": map[string]any{
				"summary":     "Read codes out of an image",
				"description": "Send the image as a multipart file field named `image`, or as a data URI.",
				"responses": map[string]any{
					"200": map[string]any{"description": "the decoded results"},
					"400": errorResponse("the image could not be read"),
					"404": errorResponse("no code was found in the image"),
					"413": errorResponse("the image exceeds the pixel or byte cap"),
				},
			},
		},
	}
}

// openAPIComponents describes the shared schemas and security schemes.
func (s *Server) openAPIComponents() map[string]any {
	return map[string]any{
		"securitySchemes": map[string]any{
			"apiKey": map[string]any{
				"type": "apiKey", "in": "header", "name": "X-API-Key",
			},
			"bearer": map[string]any{
				"type": "http", "scheme": "bearer",
			},
		},
		"schemas": map[string]any{
			"Request": s.requestSchema(),
			"Error": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"error": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"code":       map[string]any{"type": "string"},
							"message":    map[string]any{"type": "string"},
							"field":      map[string]any{"type": "string"},
							"expected":   map[string]any{"type": "string"},
							"got":        map[string]any{"type": "string"},
							"hint":       map[string]any{"type": "string"},
							"request_id": map[string]any{"type": "string"},
						},
						"required": []any{"code", "message"},
					},
				},
				"required": []any{"error"},
			},
		},
	}
}

// requestSchema describes the request body, with enums drawn from the live
// registries so the document lists exactly what this build accepts.
func (s *Server) requestSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"type": map[string]any{
				"type": "string", "enum": toAny(builder.Names()),
				"description": "selects a payload builder",
			},
			"payload": map[string]any{
				"type": "object", "additionalProperties": true,
				"description": "the builder's input; see /v1/symbologies for each builder's fields",
			},
			"data": map[string]any{
				"type": "string", "description": "the raw string to encode when no type is given",
			},
			"symbology": map[string]any{
				"type": "string", "enum": toAny(encoder.Names()),
			},
			"encode": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"ecc":        map[string]any{"type": "string", "enum": []any{"L", "M", "Q", "H"}},
					"version":    autoIntSchema("symbology version"),
					"mask":       autoIntSchema("data mask pattern"),
					"quiet_zone": map[string]any{"type": "integer", "minimum": 0},
				},
			},
			"style": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"module":     map[string]any{"type": "string", "enum": toAny(render.ModuleShapes())},
					"eye":        map[string]any{"type": "string", "enum": toAny(render.EyeShapes())},
					"eye_ball":   map[string]any{"type": "string", "enum": toAny(render.EyeShapes())},
					"fg":         colorSchema(),
					"bg":         colorSchema(),
					"eye_fg":     colorSchema(),
					"bar_height": map[string]any{"type": "integer", "minimum": 0},
					"hri":        map[string]any{"type": "boolean"},
					"hri_size": map[string]any{
						"type":    "number",
						"minimum": render.MinHRISize,
						"maximum": render.MaxHRISize,
					},
					"hri_font": map[string]any{
						"type": "string", "enum": toAny(render.HRIFonts()),
					},
					"logo":       map[string]any{"type": "string", "description": "a data: URI"},
					"logo_scale": map[string]any{"type": "number", "minimum": 0.05, "maximum": 0.35},
					"excavate":   map[string]any{"type": "boolean"},
					"caption":    map[string]any{"type": "string"},
					"frame":      map[string]any{"type": "string"},
				},
			},
			"output": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"format":  map[string]any{"type": "string", "enum": toAny(writer.Formats())},
					"scale":   map[string]any{"type": "integer", "minimum": 1},
					"size":    map[string]any{"type": "number", "minimum": 0},
					"unit":    map[string]any{"type": "string", "enum": []any{"px", "mm", "in"}},
					"dpi":     map[string]any{"type": "integer", "minimum": 1},
					"quality": map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
				},
			},
			"meta": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"filename":   map[string]any{"type": "string"},
					"attachment": map[string]any{"type": "boolean"},
				},
			},
		},
	}
}

// queryParameters lists every dot-notation key as an OpenAPI parameter,
// derived from the same reflection index the decoder uses.
func queryParameters() []any {
	keys := make([]string, 0, len(pathIndex))
	for k := range pathIndex {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]any, 0, len(keys)+1)
	for _, k := range keys {
		out = append(out, map[string]any{
			"name": k, "in": "query", "required": false,
			"schema": map[string]any{"type": openAPIType(k)},
		})
	}
	out = append(out, map[string]any{
		"name": "payload.<field>", "in": "query", "required": false,
		"description": "any field the selected builder accepts",
		"schema":      map[string]any{"type": "string"},
	})
	return out
}

// openAPIType maps a request path onto a JSON Schema type. The reflection
// index carries the Go type, but the JSON-facing names are what a client sees.
func openAPIType(key string) string {
	f, ok := pathIndex[key]
	if !ok {
		return "string"
	}
	switch f.typ.String() {
	case "int", "*int":
		return "integer"
	case "float64":
		return "number"
	case "bool", "*bool":
		return "boolean"
	case "httpapi.AutoInt":
		return "string"
	default:
		return "string"
	}
}

// symbologyPathParam describes the {symbology} path parameter.
func symbologyPathParam() []any {
	return []any{map[string]any{
		"name": "symbology", "in": "path", "required": true,
		"schema": map[string]any{"type": "string", "enum": toAny(encoder.Names())},
	}}
}

// builderPathParam describes the {type} path parameter.
func builderPathParam() []any {
	return []any{map[string]any{
		"name": "type", "in": "path", "required": true,
		"schema": map[string]any{"type": "string", "enum": toAny(builder.Names())},
	}}
}

// outputContentTypes lists a content entry per registered writer.
func (s *Server) outputContentTypes() map[string]any {
	out := make(map[string]any)
	for _, w := range writer.All() {
		schema := map[string]any{"type": "string"}
		if w.Binary() {
			schema["format"] = "binary"
		}
		out[w.MIME()] = map[string]any{"schema": schema}
	}
	return out
}

// requestBodyRef is the shared request-body description.
func requestBodyRef() map[string]any {
	return map[string]any{
		"required": false,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/Request"},
			},
			"multipart/form-data": map[string]any{
				"schema": map[string]any{
					"type": "object", "additionalProperties": true,
					"description": "the same dot-notation keys as the query string; " +
						"file parts override string fields of the same name",
				},
			},
			"application/x-www-form-urlencoded": map[string]any{
				"schema": map[string]any{"type": "object", "additionalProperties": true},
			},
		},
	}
}

// errorResponse is the shared error response description.
func errorResponse(description string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/Error"},
			},
		},
	}
}

// simpleOp describes an operation with a single JSON response.
func simpleOp(summary, description string) map[string]any {
	return map[string]any{
		"summary":     summary,
		"description": description,
		"security":    []any{},
		"responses": map[string]any{
			"200": map[string]any{"description": description},
		},
	}
}

// autoIntSchema describes an option that accepts a number or "auto".
func autoIntSchema(what string) map[string]any {
	return map[string]any{
		"description": what + `; an integer, or "auto"`,
		"oneOf": []any{
			map[string]any{"type": "integer"},
			map[string]any{"type": "string", "enum": []any{"auto"}},
		},
	}
}

// colorSchema describes a colour option.
func colorSchema() map[string]any {
	return map[string]any{
		"type":        "string",
		"description": "#rgb, #rgba, #rrggbb, #rrggbbaa, or a colour name such as transparent",
		"examples":    []any{"#111", "#00000080", "transparent"},
	}
}

// toAny widens a string slice for JSON enums.
func toAny(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}
