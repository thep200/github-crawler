#!/bin/bash

# Script to start all three consumer types (repo, release, commit)

# Make sure logs directory exists
mkdir -p logs
# Create directory for storing PIDs
mkdir -p .pids

# Function to check if a consumer is already running
check_running() {
    local consumer_type=$1
    if [ -f ".pids/$consumer_type.pid" ]; then
        local pid=$(cat ".pids/$consumer_type.pid")
        if ps -p $pid > /dev/null; then
            echo "Consumer $consumer_type is already running with PID $pid"
            return 0
        else
            # Stale PID file
            rm ".pids/$consumer_type.pid"
        fi
    fi
    return 1
}

# Function to start a consumer
start_consumer() {
    local consumer_type=$1

    # Check if already running
    if check_running $consumer_type; then
        return
    fi

    # Build consumer if necessary
    if [ ! -f "./bin/consumer" ] || [ "$(find ./cmd/consumer -newer ./bin/consumer | wc -l)" -gt 0 ]; then
        echo "Building consumer..."
        go build -o ./bin/consumer ./cmd/consumer/main.go
    fi

    # Start consumer in background
    echo "Starting $consumer_type consumer..."
    nohup ./bin/consumer -type=$consumer_type > ./logs/$consumer_type-consumer.log 2>&1 &

    # Save PID
    echo $! > ".pids/$consumer_type.pid"
    echo "Started $consumer_type consumer with PID $!"
}

# Function to stop a consumer
stop_consumer() {
    local consumer_type=$1

    if [ -f ".pids/$consumer_type.pid" ]; then
        local pid=$(cat ".pids/$consumer_type.pid")
        if ps -p $pid > /dev/null; then
            echo "Stopping $consumer_type consumer with PID $pid..."
            kill $pid
            rm ".pids/$consumer_type.pid"
        else
            echo "$consumer_type consumer is not running"
            rm ".pids/$consumer_type.pid"
        fi
    else
        echo "$consumer_type consumer is not running"
    fi
}

# Command processing
case "$1" in
    start)
        echo "Starting all consumers..."
        start_consumer repo
        start_consumer release
        start_consumer commit
        echo "All consumers started"
        ;;

    stop)
        echo "Stopping all consumers..."
        stop_consumer repo
        stop_consumer release
        stop_consumer commit
        echo "All consumers stopped"
        ;;

    restart)
        echo "Restarting all consumers..."
        stop_consumer repo
        stop_consumer release
        stop_consumer commit
        sleep 2
        start_consumer repo
        start_consumer release
        start_consumer commit
        echo "All consumers restarted"
        ;;

    status)
        # Check status of each consumer
        RUNNING=0
        for type in repo release commit; do
            if [ -f ".pids/$type.pid" ]; then
                pid=$(cat ".pids/$type.pid")
                if ps -p $pid > /dev/null; then
                    echo "✅ $type consumer is running with PID $pid"
                    RUNNING=$((RUNNING+1))
                else
                    echo "❌ $type consumer PID file exists but process is not running"
                    rm ".pids/$type.pid"
                fi
            else
                echo "❌ $type consumer is not running"
            fi
        done
        echo "$RUNNING/3 consumers running"
        ;;

    *)
        echo "Usage: $0 {start|stop|restart|status}"
        exit 1
        ;;
esac

exit 0
