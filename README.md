# Coding Assistant Leak Benchmark

## Setup
1. Start OpenAI proxy
```bash
cd ./openai_proxy
go build
./openai_proxy
```
2. Run the benchmark
```bash
go build
OPENAI_API_KEY="your_openai_key" ANTHROPIC_API_KEY="your_anthropic_key" ./deployer
```
3. Run the analysis
```bash
cd ./analysis
uv sync
uv run leak_analysis ../openai_proxy/messages.db
```
