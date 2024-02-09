# Hit ratio

### Zipf

![zipf](https://raw.githubusercontent.com/maypok86/benchmarks/main/simulator/results/zipf.png)

### S3

This trace is described as "disk read accesses initiated by a large commercial search engine in response to various web search requests.".

![s3](https://raw.githubusercontent.com/maypok86/benchmarks/main/simulator/results/s3.png)

### DS1

This trace is described as "a database server running at a commercial site running an ERP application on top of a commercial database.".

![ds1](https://raw.githubusercontent.com/maypok86/benchmarks/main/simulator/results/ds1.png)

### P3

The trace P3 was collected from workstations running Windows NT by using Vtrace
which captures disk operations through the use of device
filters.

![p3](https://raw.githubusercontent.com/maypok86/benchmarks/main/simulator/results/p3.png)

### P8

The trace P8 was collected from workstations running Windows NT by using Vtrace
which captures disk operations through the use of device
filters.

![p8](https://raw.githubusercontent.com/maypok86/benchmarks/main/simulator/results/p8.png)

### LOOP

This trace demonstrates a looping access pattern.

![loop](https://raw.githubusercontent.com/maypok86/benchmarks/main/simulator/results/loop.png)

### OLTP

This trace is described as "references to a CODASYL database for a one hour period.".

![oltp](https://raw.githubusercontent.com/maypok86/benchmarks/main/simulator/results/oltp.png)

### Conclusion

`S3-FIFO` (otter) is inferior to `W-TinyLFU` (theine) on lfu friendly traces (databases, search, analytics), but has a greater or equal hit ratio on web traces.

In summary, we have that `S3-FIFO` is competitive with `W-TinyLFU` and `ARC`. Also, it provides a substantial improvement to `LRU` across a variety of traces.
