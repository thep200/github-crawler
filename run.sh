#!/bin/bash

# Build the application
echo "Building the application..."
go build -o ./bin/crawler ./cmd/crawler/main.go
go build -o ./bin/consumer ./cmd/consumer/main.go

# Create logs directory if not exists
mkdir -p logs

# Start the consumers
echo "Starting consumers..."
./bin/consumer -type=repo > ./logs/repo-consumer.log 2>&1 &
REPO_PID=$!
echo "Repo consumer started with PID: $REPO_PID"

./bin/consumer -type=release > ./logs/release-consumer.log 2>&1 &
RELEASE_PID=$!
echo "Release consumer started with PID: $RELEASE_PID"

./bin/consumer -type=commit > ./logs/commit-consumer.log 2>&1 &
COMMIT_PID=$!
echo "Commit consumer started with PID: $COMMIT_PID"

# Wait for consumers to start
sleep 2

# Start the crawler
echo "Starting crawler v4..."
./bin/crawler -version=v4

# On exit, kill all consumers
function cleanup {
  echo "Stopping consumers..."
  kill $REPO_PID $RELEASE_PID $COMMIT_PID 2>/dev/null
}

trap cleanup EXIT
