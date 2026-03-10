#!/bin/bash
# Test script to verify /leave fix

echo "Starting test..."

# Start server
echo "Starting server..."
cd "D:\user\Deaktop\open\unity-server"
./game-server.exe > test_server.log 2>&1 &
SERVER_PID=$!
sleep 3

# Run observer bot
echo "Starting Observer bot..."
go run cmd/bot/main.go -name=Observer -room=testroom -role=observer > test_observer.log 2>&1 &
OBSERVER_PID=$!
sleep 2

# Run actor bot
echo "Starting Actor bot..."
timeout 10 go run cmd/bot/main.go -name=Actor -room=testroom -role=actor > test_actor.log 2>&1
RESULT=$?

# Check results
echo ""
echo "=== Test Results ==="
if grep -q "VERIFY: Passed - Received Leave" test_observer.log; then
    echo "✓ Leave push received"
else
    echo "✗ Leave push NOT received"
fi

if grep -q "Left Room:" test_actor.log; then
    echo "✓ Leave response received"
else
    echo "✗ Leave response NOT received"
fi

if grep -q "ALL TESTS PASSED" test_observer.log; then
    echo "✓ ALL TESTS PASSED"
else
    echo "✗ Some tests failed"
fi

# Cleanup
kill $SERVER_PID $OBSERVER_PID 2>/dev/null
echo ""
echo "Test complete. Check test_*.log for details."
