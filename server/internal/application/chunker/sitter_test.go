package chunker_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/suite"

	"github.com/makifbaysal/tasktrooper/server/internal/application/chunker"
)

type SitterChunkerSuite struct {
	suite.Suite
}

func TestSitterChunkerSuite(t *testing.T) {
	suite.Run(t, new(SitterChunkerSuite))
}

// symbolKinds indexes chunks by symbol name so a test can assert on the shape
// of one declaration without depending on chunk order.
func symbolKinds(chunks []chunker.Chunk) map[string]string {
	out := make(map[string]string, len(chunks))
	for _, ch := range chunks {
		out[ch.SymbolName] = ch.Kind
	}
	return out
}

func (s *SitterChunkerSuite) TestTSXComponentsAndClassMethods() {
	src := []byte(`import { useState } from "react";

export default function Page({ id }: { id: string }) {
  return <div>{id}</div>;
}

export const Card = ({ title }: Props) => <section>{title}</section>;

export class Widget {
  render() {
    return null;
  }
}

export type Props = { title: string };
`)
	chunks, err := chunker.TSChunker{}.Chunk("src/components/Page.tsx", src)
	s.Require().NoError(err)

	kinds := symbolKinds(chunks)
	s.Equal("function", kinds["Page"])
	s.Equal("function", kinds["Card"])
	s.Equal("class", kinds["Widget"])
	s.Equal("method", kinds["Widget.render"])
	s.Equal("type", kinds["Props"])
	for _, ch := range chunks {
		s.Equal("typescript", ch.Language)
	}
}

func (s *SitterChunkerSuite) TestTSSignatureAndContent() {
	src := []byte(`export async function loadUser(id: string): Promise<User> {
  return db.users.find(id);
}
`)
	chunks, err := chunker.TSChunker{}.Chunk("src/lib/api.ts", src)
	s.Require().NoError(err)
	s.Require().Len(chunks, 1)
	s.Equal("loadUser", chunks[0].SymbolName)
	s.Contains(chunks[0].Signature, "loadUser(id: string): Promise<User>")
	s.Contains(chunks[0].Content, "db.users.find(id)")
}

// A long signature is clipped, and the cut must not land inside a multi-byte
// rune: Postgres refuses the half character ("invalid byte sequence for
// encoding UTF8") and the whole index run fails with it.
func (s *SitterChunkerSuite) TestLongSignatureClipsOnRuneBoundary() {
	params := strings.Repeat("ı", 150)
	// Two names of different parity so one of them puts the cut mid-rune.
	src := []byte("export function f(" + params + ": string) {\n  return 1;\n}\n\n" +
		"export function fx(" + params + ": string) {\n  return 2;\n}\n")
	chunks, err := chunker.TSChunker{}.Chunk("src/lib/uzun.ts", src)
	s.Require().NoError(err)
	s.Require().NotEmpty(chunks)
	for _, ch := range chunks {
		s.True(utf8.ValidString(ch.Signature), "signature of %s is not valid UTF-8: %q", ch.SymbolName, ch.Signature)
		s.True(strings.HasSuffix(ch.Signature, "…"), "signature of %s was not clipped", ch.SymbolName)
	}
}

func (s *SitterChunkerSuite) TestSwiftStructProtocolAndMembers() {
	src := []byte(`import SwiftUI

struct TaskListView: View {
    @State private var items: [Item] = []

    var body: some View { Text("hi") }

    func reload() async throws {
        items = []
    }
}

final class Store: ObservableObject {
    init(seed: Int) {}
    func fetch() -> [Item] { [] }
}

protocol Loader { func load() async }

func freeFunction(_ a: Int) -> Int { a }
`)
	chunks, err := chunker.SwiftChunker{}.Chunk("Sources/TaskListView.swift", src)
	s.Require().NoError(err)

	kinds := symbolKinds(chunks)
	s.Equal("struct", kinds["TaskListView"])
	s.Equal("method", kinds["TaskListView.reload"])
	s.Equal("class", kinds["Store"])
	s.Equal("method", kinds["Store.init"])
	s.Equal("method", kinds["Store.fetch"])
	s.Equal("protocol", kinds["Loader"])
	s.Equal("function", kinds["freeFunction"])
}

func (s *SitterChunkerSuite) TestKotlinClassObjectAndTopLevelFunction() {
	src := []byte(`package com.example

class Repo(private val api: Api) {
    fun load(id: String): Item = api.get(id)

    suspend fun save(item: Item) {
        api.put(item)
    }
}

object Singleton {
    fun go() {}
}

interface Api {
    fun get(id: String): Item
}

fun topLevel(x: Int): Int = x + 1
`)
	chunks, err := chunker.KotlinChunker{}.Chunk("app/src/main/kotlin/Repo.kt", src)
	s.Require().NoError(err)

	kinds := symbolKinds(chunks)
	s.Equal("class", kinds["Repo"])
	s.Equal("method", kinds["Repo.load"])
	s.Equal("method", kinds["Repo.save"])
	s.Equal("object", kinds["Singleton"])
	s.Equal("method", kinds["Singleton.go"])
	s.Equal("interface", kinds["Api"])
	s.Equal("function", kinds["topLevel"])
}

func (s *SitterChunkerSuite) TestJavaClassMembers() {
	src := []byte(`package com.example;

public class Greeter implements Speaker {
    private String name;

    public Greeter(String name) {
        this.name = name;
    }

    public String hello(int times) {
        return "hi";
    }
}

interface Speaker {
    String hello(int times);
}
`)
	chunks, err := chunker.JavaChunker{}.Chunk("src/main/java/Greeter.java", src)
	s.Require().NoError(err)

	kinds := symbolKinds(chunks)
	s.Equal("class", kinds["Greeter"])
	s.Equal("method", kinds["Greeter.Greeter"])
	s.Equal("method", kinds["Greeter.hello"])
	s.Equal("interface", kinds["Speaker"])
}

func (s *SitterChunkerSuite) TestPythonDecoratedFunctionAndClass() {
	src := []byte(`import os


@app.route("/health")
def health():
    return "ok"


class Worker:
    def run(self):
        return 1
`)
	chunks, err := chunker.PythonChunker{}.Chunk("scripts/run.py", src)
	s.Require().NoError(err)

	kinds := symbolKinds(chunks)
	s.Equal("function", kinds["health"])
	s.Equal("class", kinds["Worker"])
	s.Equal("method", kinds["Worker.run"])

	for _, ch := range chunks {
		if ch.SymbolName == "health" {
			s.Contains(ch.Content, `@app.route("/health")`)
		}
	}
}

// A file whose bulk sits outside any declaration — a route handler built from
// one giant literal — must still be indexed end to end, which is what the old
// fixed-size fallback did and what the symbol walk alone would drop.
func (s *SitterChunkerSuite) TestTopLevelGapIsCovered() {
	var b strings.Builder
	b.WriteString("export function tiny() { return 1 }\n\nconst HTML = `\n")
	for i := 0; i < 120; i++ {
		b.WriteString("  <p>line of markup</p>\n")
	}
	b.WriteString("`;\n")

	chunks, err := chunker.TSChunker{}.Chunk("src/app/route.ts", []byte(b.String()))
	s.Require().NoError(err)

	covered := 0
	for _, ch := range chunks {
		covered += ch.EndLine - ch.StartLine + 1
	}
	s.Greater(covered, 100, "large top-level literal was not chunked")
}

func (s *SitterChunkerSuite) TestUnparsableFileFallsBackInsteadOfFailing() {
	chunks, err := chunker.SwiftChunker{}.Chunk("Broken.swift", []byte("this is not swift at all !!!\n"))
	s.Require().NoError(err)
	s.Require().NotEmpty(chunks)
}

func (s *SitterChunkerSuite) TestRegistryCoversEveryIndexableLanguage() {
	reg := chunker.DefaultRegistry()
	cases := map[string]string{
		"a.ts":    "typescript",
		"a.tsx":   "typescript",
		"a.js":    "javascript",
		"a.py":    "python",
		"a.java":  "java",
		"a.kt":    "kotlin",
		"a.swift": "swift",
	}
	sources := map[string]string{
		"typescript": "export function run() { return 1 }\n",
		"javascript": "export function run() { return 1 }\n",
		"python":     "def run():\n    return 1\n",
		"java":       "class A { void run() {} }\n",
		"kotlin":     "fun run(): Int = 1\n",
		"swift":      "func run() -> Int { 1 }\n",
	}
	for path, lang := range cases {
		chunks, err := reg.Chunk(path, []byte(sources[lang]))
		s.Require().NoErrorf(err, "chunking %s", path)
		s.Require().NotEmptyf(chunks, "no chunks for %s", path)
		found := false
		for _, ch := range chunks {
			// Java's run() is a member, so it is named A.run there.
			if ch.SymbolName == "run" || strings.HasSuffix(ch.SymbolName, ".run") {
				found = true
				s.Equalf(lang, ch.Language, "unexpected language for %s", path)
			}
		}
		s.Truef(found, "no run symbol for %s", path)
	}
}
