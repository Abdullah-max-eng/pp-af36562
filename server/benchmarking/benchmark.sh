#!/bin/bash

#simple benchmark.sh scrip

SERVER_URL="http://localhost:8081/query"  # Update this if your server listens on a different port
OUTPUT_FILE="results.csv"                 # CSV output file where final results are stored
QUERIES_FILE="queries.txt"                # File containing one Cypher query per line
RUNS=100                                   # How many times each query should be executed

# creating a file to put the results in in and adding the header 
echo "query_id,average_time_ms" > "$OUTPUT_FILE"

query_id=1 

# Read each query line-by-line from the file
while IFS= read -r query; do
    echo "Benchmarking query $query_id..."

    total_time_ns=0  # accumulator for total run time of this query (in nanoseconds)

    # Run the same query multiple times to smooth out noise
    for ((i=1; i<=RUNS; i++)); do
        # Capture timestamp before request
        start=$(date +%s%N)

        # Hit the server with the current query
        curl -s -X POST "$SERVER_URL" \
            -H "Content-Type: application/json" \
            -d "{\"cypher\":\"$query\"}" > /dev/null

        # Capture timestamp after request
        end=$(date +%s%N)

        # time spent
        elapsed=$((end - start))




        total_time_ns=$((total_time_ns + elapsed))
    done

    # avrage tiem)
    avg_time_ns=$((total_time_ns / RUNS))
    avg_time_ms=$(echo "scale=3; $avg_time_ns / 1000000" | bc)



    # appending the results 
    echo "$query_id,$avg_time_ms" >> "$OUTPUT_FILE"

    # Move to next query in the list
    ((query_id++))

done < "$QUERIES_FILE"

echo "Benchmark completed. Results saved in $OUTPUT_FILE"
