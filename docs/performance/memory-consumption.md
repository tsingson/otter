# Memory consumption

### Description

These benchmarks use pre-generated strings as keys and values, i.e., each key-value pair occupies 32 bytes. The implementations are run with the expiration policy enabled to check the amount of memory it consumes. This is also the most common cache usage scenario.

The source code can be found [here](https://github.com/maypok86/benchmarks/blob/main/memory/main.go), and the results in text form [here](https://github.com/maypok86/benchmarks/blob/main/memory/results/memory.txt).

### Capacity (10000)

In this benchmark, the cache capacity is 10000 elements.

![memory_10000](https://raw.githubusercontent.com/maypok86/benchmarks/main/memory/results/memory_10000.png)

### Capacity (25000)

In this benchmark, the cache capacity is 25000 elements.

![memory_25000](https://raw.githubusercontent.com/maypok86/benchmarks/main/memory/results/memory_25000.png)

### Capacity (100000)

In this benchmark, the cache capacity is 100000 elements.

![memory_100000](https://raw.githubusercontent.com/maypok86/benchmarks/main/memory/results/memory_100000.png)

### Capacity (1000000)

In this benchmark, the cache capacity is 1000000 elements.

![memory_1000000](https://raw.githubusercontent.com/maypok86/benchmarks/main/memory/results/memory_1000000.png)

### Conclusion

At small cache sizes, the buffers used in otter slightly worsen memory consumption, but as the cache size increases, the per entry overhead affects memory consumption much more, which is why otter starts to show great results.

Also, ristretto's performance is questionable, as its per entry overhead is quite high. This is most likely caused by the bloom filter used in it, which rejects a lot of items in advance or it is another consequence of a bug that causes poor hit ratio.
