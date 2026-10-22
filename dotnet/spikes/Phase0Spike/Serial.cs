using System.Diagnostics;
using System.IO.Ports;

// Serial spike: opens the port with DTR/RTS asserted (what go.bug.st/serial
// does by default when Mode.InitialStatusBits is nil), waits the boot-settle
// delay, sends "A\r" and reads until 0x0D — same framing as protocol.go.
static class SerialSpike
{
    public static void Run(string path, int baud, int bootDelayMs)
    {
        using var port = new SerialPort(path, baud, Parity.None, 8, StopBits.One)
        {
            DtrEnable = true,
            RtsEnable = true,
            ReadTimeout = 3000,
            WriteTimeout = 3000,
        };
        port.Open();
        Console.WriteLine($"opened {path} @ {baud}, DTR={port.DtrEnable} RTS={port.RtsEnable}; waiting {bootDelayMs}ms for device boot");
        Thread.Sleep(bootDelayMs);

        for (var i = 1; i <= 3; i++)
        {
            port.DiscardInBuffer();
            port.Write("A\r");
            var buf = new List<byte>();
            var sw = Stopwatch.StartNew();
            var chunk = new byte[256];
            while (sw.ElapsedMilliseconds < 3000)
            {
                int n;
                try { n = port.Read(chunk, 0, chunk.Length); }
                catch (TimeoutException) { break; }
                buf.AddRange(chunk.AsSpan(0, n).ToArray());
                if (buf.Count > 0 && buf[^1] == 0x0D) break;
            }
            Console.WriteLine($"  attempt {i}: {buf.Count} bytes in {sw.ElapsedMilliseconds}ms: {Convert.ToHexString(buf.ToArray())}");
        }
    }
}
