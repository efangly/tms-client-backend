using System.Net;
using System.Net.Sockets;
using Microsoft.Extensions.Logging.Abstractions;
using TmsBackend.Transport;

namespace TmsBackend.Tests.Transport;

/// <summary>Minimal stand-in for a network probe: answers each command with the next scripted frame.</summary>
internal sealed class FakeProbeServer : IAsyncDisposable
{
    private readonly TcpListener _listener = new(IPAddress.Loopback, 0);
    private readonly Queue<byte[]> _frames;
    private readonly CancellationTokenSource _cts = new();
    private readonly Task _loop;

    public FakeProbeServer(params byte[][] frames)
    {
        _frames = new Queue<byte[]>(frames);
        _listener.Start();
        _loop = Task.Run(AcceptLoop);
    }

    public int Port => ((IPEndPoint)_listener.LocalEndpoint).Port;
    public List<string> Commands { get; } = [];

    private async Task AcceptLoop()
    {
        try
        {
            while (!_cts.IsCancellationRequested)
            {
                using var client = await _listener.AcceptTcpClientAsync(_cts.Token);
                var stream = client.GetStream();
                var buf = new byte[64];
                var n = await stream.ReadAsync(buf, _cts.Token);
                lock (Commands)
                    Commands.Add(System.Text.Encoding.ASCII.GetString(buf, 0, n));
                if (_frames.TryDequeue(out var frame))
                    await stream.WriteAsync(frame, _cts.Token);
            }
        }
        catch (OperationCanceledException)
        {
        }
    }

    public async ValueTask DisposeAsync()
    {
        _cts.Cancel();
        _listener.Stop();
        try { await _loop; } catch (Exception) { /* listener torn down */ }
    }
}

public class TcpProbeClientTests
{
    private static readonly byte[] NineByteFrame = [0x41, 0x41, 0x5a, 0x00, 0x5a, 0x19, 0xa3, 0x5a, 0x0d];
    private static readonly byte[] BrokenSensorFrame = [0x41, 0x41, 0x5a, 0x00, 0x5a, 0xFF, 0xFF, 0x5a, 0x0d];

    private static TcpProbeClient Client() => new(NullLogger<TcpProbeClient>.Instance);

    [Fact]
    public async Task Reads_and_parses_one_frame()
    {
        await using var server = new FakeProbeServer(NineByteFrame);

        var r = await Client().RequestAsync("127.0.0.1", server.Port, "", TimeSpan.FromSeconds(2));

        Assert.True(r.Connected);
        Assert.Equal("", r.Error);
        Assert.Equal("41415a005a19a35a0d", r.Data);
        Assert.Equal(25.63, Assert.Single(r.Probes).TempValue);
        Assert.Equal(["A\r"], server.Commands);
    }

    [Fact]
    public async Task Retries_anomalous_readings_up_to_three_times()
    {
        await using var server = new FakeProbeServer(BrokenSensorFrame, BrokenSensorFrame, NineByteFrame);

        var r = await Client().RequestAsync("127.0.0.1", server.Port, "A", TimeSpan.FromSeconds(2));

        Assert.Equal(3, server.Commands.Count);
        Assert.Equal(6563, Assert.Single(r.Probes).RealValue);
    }

    [Fact]
    public async Task Gives_up_after_three_anomalous_readings()
    {
        await using var server = new FakeProbeServer(BrokenSensorFrame, BrokenSensorFrame, BrokenSensorFrame, NineByteFrame);

        var r = await Client().RequestAsync("127.0.0.1", server.Port, "A", TimeSpan.FromSeconds(2));

        Assert.Equal(3, server.Commands.Count);
        Assert.Empty(r.Probes);
    }

    [Fact]
    public async Task Connection_refused_reports_error_without_retry()
    {
        // Grab a free port, then close it so nothing is listening.
        var l = new TcpListener(IPAddress.Loopback, 0);
        l.Start();
        var port = ((IPEndPoint)l.LocalEndpoint).Port;
        l.Stop();

        var r = await Client().RequestAsync("127.0.0.1", port, "A", TimeSpan.FromMilliseconds(500));

        Assert.False(r.Connected);
        Assert.StartsWith("Connection failed:", r.Error);
        Assert.Empty(r.Probes);
    }

    [Fact]
    public async Task Silent_device_returns_no_probes_after_timeout()
    {
        await using var server = new FakeProbeServer(); // accepts, never answers

        var r = await Client().RequestAsync("127.0.0.1", server.Port, "A", TimeSpan.FromMilliseconds(300));

        Assert.True(r.Connected);
        Assert.Equal("", r.Data);
        Assert.Empty(r.Probes);
    }
}
