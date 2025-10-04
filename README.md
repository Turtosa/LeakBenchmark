# Coding Assistant Leak Benchmark

## Setup
1. Start claude-code-proxy
```bash
cd ./claude-code-proxy
uv sync
OPENAI_API_KEY="YOUR_OPENAI_KEY" uv run claude-code-proxy
```
2. Start OpenAI proxy
```bash
cd ./openai_proxy
go build
./openai_proxy
```
3. Run the benchmark
```bash
go build
./deployer
```
4. Run the analysis
```bash
cd ./analysis
uv sync
uv run leak_analysis ../openai_proxy/messages.db
```
