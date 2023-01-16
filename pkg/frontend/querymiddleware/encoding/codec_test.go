// SPDX-License-Identifier: AGPL-3.0-only

package encoding

import (
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/grafana/mimir/pkg/frontend/querymiddleware"
)

var knownCodecs = map[string]Codec{
	"original JSON":               OriginalJsonCodec{},
	"uninterned protobuf":         UninternedProtobufCodec{},
	"interned protobuf":           InternedProtobufCodec{},
	"gzipped uninterned protobuf": GzipWrapperCodec{UninternedProtobufCodec{}},
}

// This directory contains a selection of query results from an internal operational cluster
// at Grafana Labs, and so can't be shared publicly.
// It contains two subdirectories: one named "ruler" for rule evaluation results, and another named "querier" for general query results.
// You can capture equivalent data from your own cluster with the evaluate-rules and evaluate-query-log tools in the tools directory.
const sourceDir = "/Users/charleskorn/Desktop/queries/all"

func TestEncodingRoundtrip(t *testing.T) {
	originalFileNames, err := recursivelyFindFilesWithSuffix(sourceDir, ".json")
	require.NoError(t, err)
	require.NotEmpty(t, originalFileNames)

	originalJsonCodec := OriginalJsonCodec{}

	for _, originalFileName := range originalFileNames {
		relativeName, err := filepath.Rel(sourceDir, originalFileName)
		require.NoError(t, err)

		t.Run(relativeName, func(t *testing.T) {
			originalBytes, err := os.ReadFile(originalFileName)
			require.NoError(t, err)

			original, err := originalJsonCodec.Decode(originalBytes)
			require.NoError(t, err)

			for name, codec := range knownCodecs {
				t.Run(name, func(t *testing.T) {
					encoded, err := codec.Encode(original)
					require.NoError(t, err)

					decoded, err := codec.Decode(encoded)
					require.NoError(t, err)
					requireEqual(t, original, decoded)
				})
			}
		})
	}
}

func requireEqual(t *testing.T, expected querymiddleware.PrometheusResponse, actual querymiddleware.PrometheusResponse) {
	require.Equal(t, expected.Status, actual.Status)
	require.Equal(t, expected.ErrorType, actual.ErrorType)
	require.Equal(t, expected.Error, actual.Error)
	require.Equal(t, expected.Headers, actual.Headers)
	require.Equal(t, expected.Data.ResultType, actual.Data.ResultType)
	require.Len(t, actual.Data.Result, len(expected.Data.Result))

	for streamIdx, actualStream := range actual.Data.Result {
		expectedStream := expected.Data.Result[streamIdx]

		require.ElementsMatch(t, expectedStream.Labels, actualStream.Labels)
		require.Len(t, actualStream.Samples, len(expectedStream.Samples))

		for sampleIdx, actualSample := range actualStream.Samples {
			expectedSample := expectedStream.Samples[sampleIdx]

			if math.IsNaN(expectedSample.Value) && math.IsNaN(actualSample.Value) {
				// NaN != NaN, so we can't assert that the two points are the same if both have NaN values.
				// So we have to check the timestamp separately.
				require.Equal(t, expectedSample.TimestampMs, actualSample.TimestampMs)
			} else {
				require.Equal(t, expectedSample, actualSample)
			}
		}
	}
}

func BenchmarkDecodeAll(b *testing.B) {
	directories, err := os.ReadDir(sourceDir)
	require.NoError(b, err)

	originalJsonCodec := OriginalJsonCodec{}
	codec := getCodec(b)

	for _, directory := range directories {
		if !directory.IsDir() {
			continue
		}

		files, err := recursivelyFindFilesWithSuffix(path.Join(sourceDir, directory.Name()), ".json")
		require.NoError(b, err)
		require.NotEmpty(b, files)

		samples := make([][]byte, 0, len(files))

		for _, file := range files {
			jsonBytes, err := os.ReadFile(file)
			require.NoError(b, err)

			resp, err := originalJsonCodec.Decode(jsonBytes)
			require.NoError(b, err)

			encodedBytes, err := codec.Encode(resp)
			require.NoError(b, err)

			samples = append(samples, encodedBytes)
		}

		b.Run(directory.Name(), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				for _, sample := range samples {
					_, err := codec.Decode(sample)

					if err != nil {
						require.NoError(b, err)
					}
				}
			}
		})
	}
}

func BenchmarkDecodeExamples(b *testing.B) {
	files, err := recursivelyFindFilesWithSuffix("testdata", ".json")
	require.NoError(b, err)
	require.NotEmpty(b, files)

	originalJsonCodec := OriginalJsonCodec{}
	codec := getCodec(b)

	for _, file := range files {
		jsonBytes, err := os.ReadFile(file)
		require.NoError(b, err)

		resp, err := originalJsonCodec.Decode(jsonBytes)
		require.NoError(b, err)

		encodedBytes, err := codec.Encode(resp)
		require.NoError(b, err)

		b.Run(file, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_, err := codec.Decode(encodedBytes)

				if err != nil {
					require.NoError(b, err)
				}
			}
		})
	}
}

func BenchmarkEncodeAll(b *testing.B) {
	directories, err := os.ReadDir(sourceDir)
	require.NoError(b, err)

	originalJsonCodec := OriginalJsonCodec{}
	codec := getCodec(b)

	for _, directory := range directories {
		if !directory.IsDir() {
			continue
		}

		files, err := recursivelyFindFilesWithSuffix(path.Join(sourceDir, directory.Name()), ".json")
		require.NoError(b, err)
		require.NotEmpty(b, files)

		samples := make([]querymiddleware.PrometheusResponse, 0, len(files))

		for _, file := range files {
			jsonBytes, err := os.ReadFile(file)
			require.NoError(b, err)

			resp, err := originalJsonCodec.Decode(jsonBytes)
			require.NoError(b, err)

			samples = append(samples, resp)
		}

		b.Run(directory.Name(), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				for _, resp := range samples {
					_, err := codec.Encode(resp)

					if err != nil {
						require.NoError(b, err)
					}
				}
			}
		})
	}
}

func BenchmarkEncodeExamples(b *testing.B) {
	files, err := recursivelyFindFilesWithSuffix("testdata", ".json")
	require.NoError(b, err)
	require.NotEmpty(b, files)

	originalJsonCodec := OriginalJsonCodec{}
	codec := getCodec(b)

	for _, file := range files {
		jsonBytes, err := os.ReadFile(file)
		require.NoError(b, err)

		resp, err := originalJsonCodec.Decode(jsonBytes)
		require.NoError(b, err)

		b.Run(file, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_, err := codec.Encode(resp)

				if err != nil {
					require.NoError(b, err)
				}
			}
		})
	}
}

func recursivelyFindFilesWithSuffix(dir string, suffix string) ([]string, error) {
	files := make([]string, 0)

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() || !strings.HasSuffix(path, suffix) {
			return nil
		}

		files = append(files, path)

		return nil
	})

	if err != nil {
		return nil, err
	}

	return files, nil
}

func getCodec(b require.TestingT) Codec {
	name := os.Getenv("CODEC")
	require.NotEmpty(b, name, "the CODEC environment variable is not set")
	require.Contains(b, knownCodecs, name, "the CODEC environment variable is set to an unknown codec name")

	return knownCodecs[name]
}