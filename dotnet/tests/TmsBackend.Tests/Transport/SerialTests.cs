using Microsoft.Extensions.Logging.Abstractions;
using TmsBackend.Config;
using TmsBackend.Data;
using TmsBackend.Infrastructure;
using TmsBackend.Models;
using TmsBackend.Transport;
using TmsBackend.Transport.Serial;

namespace TmsBackend.Tests.Transport;

/// <summary>Scripted port: each Read returns the next frame; can fail writes/reads on demand.</summary>
internal sealed class FakeSerialPort : ISerialPortIo
{
    public Exception? WriteError { get; set; }
    public Exception? ReadError { get; set; }
    public Queue<byte[]> Responses { get; } = new();
    public List<string> Written { get; } = [];
    public bool Disposed { get; private set; }

    public void DiscardInBuffer() { }

    public void Write(byte[] data)
    {
        if (WriteError != null)
            throw WriteError;
        lock (Written)
            Written.Add(System.Text.Encoding.ASCII.GetString(data));
    }

    public int Read(byte[] buffer, TimeSpan timeout)
    {
        if (ReadError != null)
            throw ReadError;
        if (!Responses.TryDequeue(out var frame))
            return 0; // timeout
        frame.CopyTo(buffer, 0);
        return frame.Length;
    }

    public void Dispose() => Disposed = true;
}

// Ported from internal/serialclient/{serialclient,protocol}_test.go.
public class SerialConnectionTests
{
    private static readonly byte[] NineByteFrame = [0x41, 0x41, 0x5a, 0x00, 0x5a, 0x19, 0xa3, 0x5a, 0x0d];
    private static readonly byte[] BrokenSensorFrame = [0x41, 0x41, 0x5a, 0x00, 0x5a, 0xFF, 0xFF, 0x5a, 0x0d];

    private static SerialConnection NewConnection(bool autoReconnect = false, Func<SerialPortConfig, ISerialPortIo>? open = null) =>
        new(new SerialPortConfig
        {
            Path = "COM_TEST",
            AutoReconnect = autoReconnect,
            ReconnectInterval = TimeSpan.FromHours(1),
            BootSettleDelay = TimeSpan.Zero,
        }, open ?? (_ => throw new IOException("port does not exist")), NullLogger.Instance);

    private static (SerialConnection, FakeSerialPort) Connected(bool autoReconnect = false)
    {
        var fake = new FakeSerialPort();
        var conn = NewConnection(autoReconnect);
        conn.SetConnectedForTest(fake);
        return (conn, fake);
    }

    [Fact]
    public async Task Single_probe_success()
    {
        var (conn, fake) = Connected();
        fake.Responses.Enqueue(NineByteFrame);

        var r = await conn.RequestAsync("A", TimeSpan.FromMilliseconds(100));

        Assert.Equal("", r.Error);
        Assert.True(r.Connected);
        Assert.Equal(25.63, Assert.Single(r.Probes).TempValue);
        Assert.Equal(["A\r"], fake.Written);
    }

    [Fact]
    public async Task Empty_command_defaults_to_A()
    {
        var (conn, fake) = Connected();
        fake.Responses.Enqueue(NineByteFrame);
        await conn.RequestAsync("", TimeSpan.FromMilliseconds(100));
        Assert.Equal(["A\r"], fake.Written);
    }

    [Fact]
    public async Task Not_connected_returns_error()
    {
        var r = await NewConnection().RequestAsync("A", TimeSpan.FromMilliseconds(100));
        Assert.NotEqual("", r.Error);
        Assert.False(r.Connected);
    }

    [Fact]
    public async Task Write_error_marks_disconnected()
    {
        var (conn, fake) = Connected();
        fake.WriteError = new IOException("simulated write failure");

        var r = await conn.RequestAsync("A", TimeSpan.FromMilliseconds(100));

        Assert.Contains("simulated write failure", r.Error);
        Assert.False(conn.IsConnected);
        Assert.True(fake.Disposed);
    }

    [Fact]
    public async Task Read_error_marks_disconnected()
    {
        var (conn, fake) = Connected();
        fake.ReadError = new IOException("simulated read failure");

        var r = await conn.RequestAsync("A", TimeSpan.FromMilliseconds(100));

        Assert.Contains("simulated read failure", r.Error);
        Assert.False(conn.IsConnected);
    }

    [Fact]
    public async Task Retries_on_anomalous_reading()
    {
        var (conn, fake) = Connected();
        fake.Responses.Enqueue(BrokenSensorFrame);
        fake.Responses.Enqueue(BrokenSensorFrame);
        fake.Responses.Enqueue(NineByteFrame);

        var r = await conn.RequestAsync("A", TimeSpan.FromMilliseconds(100));

        Assert.Equal(3, fake.Written.Count);
        Assert.Equal(6563, Assert.Single(r.Probes).RealValue);
    }

    [Fact]
    public async Task Concurrent_requests_do_not_interleave()
    {
        var (conn, fake) = Connected();
        for (var i = 0; i < 10; i++)
            fake.Responses.Enqueue(NineByteFrame);

        var results = await Task.WhenAll(Enumerable.Range(0, 10).Select(_ => conn.RequestAsync("A", TimeSpan.FromMilliseconds(100))));

        Assert.All(results, r => Assert.Single(r.Probes));
    }

    [Fact]
    public void Connect_to_missing_port_throws_and_stays_disconnected()
    {
        var conn = NewConnection();
        Assert.ThrowsAny<Exception>(conn.Connect);
        Assert.False(conn.IsConnected);
        Assert.False(conn.HasPendingReconnect);
    }

    [Fact]
    public void Failed_connect_with_auto_reconnect_schedules_retry()
    {
        var conn = NewConnection(autoReconnect: true);
        Assert.ThrowsAny<Exception>(conn.Connect);
        Assert.True(conn.HasPendingReconnect);
        conn.Dispose();
    }

    [Fact]
    public void Double_connect_is_a_no_op()
    {
        var opens = 0;
        var conn = NewConnection(open: _ => { opens++; return new FakeSerialPort(); });
        conn.Connect();
        conn.Connect();
        Assert.Equal(1, opens);
        Assert.True(conn.IsConnected);
    }

    [Fact]
    public void Io_error_schedules_reconnect_only_when_enabled()
    {
        var (enabled, _) = Connected(autoReconnect: true);
        enabled.HandleIOError(new IOException("x"));
        Assert.False(enabled.IsConnected);
        Assert.True(enabled.HasPendingReconnect);
        enabled.Dispose();

        var (disabled, _) = Connected(autoReconnect: false);
        disabled.HandleIOError(new IOException("x"));
        Assert.False(disabled.HasPendingReconnect);
    }

    [Fact]
    public void Disconnect_cancels_pending_reconnect_and_prevents_new_ones()
    {
        var (conn, _) = Connected(autoReconnect: true);
        conn.HandleIOError(new IOException("x"));
        Assert.True(conn.HasPendingReconnect);

        conn.Disconnect();
        Assert.False(conn.HasPendingReconnect);
        Assert.True(conn.IsStopped);

        conn.HandleIOError(new IOException("after stop"));
        Assert.False(conn.HasPendingReconnect);
    }

    [Fact]
    public async Task Reconnect_timer_reopens_the_port()
    {
        var attempts = 0;
        var conn = new SerialConnection(new SerialPortConfig
        {
            Path = "COM_TEST",
            AutoReconnect = true,
            ReconnectInterval = TimeSpan.FromMilliseconds(20),
            BootSettleDelay = TimeSpan.Zero,
        }, _ => ++attempts < 3 ? throw new IOException("not yet") : new FakeSerialPort(), NullLogger.Instance);

        Assert.ThrowsAny<Exception>(conn.Connect);
        var deadline = DateTime.UtcNow.AddSeconds(5);
        while (!conn.IsConnected && DateTime.UtcNow < deadline)
            await Task.Delay(10);

        Assert.True(conn.IsConnected);
        Assert.Equal(3, attempts);
        conn.Dispose();
    }
}

internal sealed class FakeMachineRepository(params MasterMachine[] machines) : IMachineRepository
{
    public Exception? GetAllError { get; set; }
    public List<MasterMachine> Machines { get; } = [.. machines];

    public Task<List<MasterMachine>> GetAllAsync(CancellationToken ct = default) =>
        GetAllError != null ? Task.FromException<List<MasterMachine>>(GetAllError) : Task.FromResult(Machines.ToList());

    public Task<MasterMachine?> GetAsync(string machineIp, int probeNo, CancellationToken ct = default) =>
        Task.FromResult(Machines.FirstOrDefault(m => m.MachineIP == machineIp && m.ProbeNo == probeNo));

    public Task<MasterMachine?> GetFirstByIpAsync(string machineIp, CancellationToken ct = default) =>
        Task.FromResult(Machines.Where(m => m.MachineIP == machineIp).OrderBy(m => m.ProbeNo).FirstOrDefault());

    public Exception? WriteError { get; set; }

    public Task CreateAsync(MasterMachine machine, CancellationToken ct = default)
    {
        if (WriteError != null)
            return Task.FromException(WriteError);
        Machines.Add(machine);
        return Task.CompletedTask;
    }

    public List<(string Ip, int ProbeNo, Dictionary<string, object?> Columns)> Updates { get; } = [];

    public Task UpdateAsync(string machineIp, int probeNo, IReadOnlyDictionary<string, object?> columns, CancellationToken ct = default)
    {
        if (WriteError != null)
            return Task.FromException(WriteError);
        Updates.Add((machineIp, probeNo, columns.ToDictionary()));
        var m = Machines.FirstOrDefault(x => x.MachineIP == machineIp && x.ProbeNo == probeNo);
        if (m != null)
        {
            foreach (var (col, val) in columns)
            {
                switch (col)
                {
                    case "machine_name": m.MachineName = (string)val!; break;
                    case "color": m.Color = (string)val!; break;
                    case "min_temp": m.MinTemp = (double?)val; break;
                    case "max_temp": m.MaxTemp = (double?)val; break;
                    case "chkReport": m.ChkReport = (string)val!; break;
                }
            }
        }
        return Task.CompletedTask;
    }

    public List<(string Ip, int? ProbeNo)> Deletes { get; } = [];

    public Task DeleteAsync(string machineIp, int? probeNo, CancellationToken ct = default)
    {
        Deletes.Add((machineIp, probeNo));
        Machines.RemoveAll(m => m.MachineIP == machineIp && (probeNo == null || m.ProbeNo == probeNo));
        return Task.CompletedTask;
    }

    public List<(string Ip, int ProbeNo, string Value)> ChkMonUpdates { get; } = [];

    public Task UpdateColorAsync(string machineIp, int probeNo, string color, CancellationToken ct = default) =>
        UpdateAsync(machineIp, probeNo, new Dictionary<string, object?> { ["color"] = color }, ct);

    public List<LatestReading> LatestReadings { get; } = [];

    public Task UpdateChkMonAsync(string machineIp, int probeNo, string value, CancellationToken ct = default)
    {
        ChkMonUpdates.Add((machineIp, probeNo, value));
        return Task.CompletedTask;
    }

    public Task<List<LatestReading>> GetLatestReadingsAsync(CancellationToken ct = default) => Task.FromResult(LatestReadings.ToList());
}

// Ported from internal/services/{serial_service,request_probes}_test.go.
public class SerialServiceTests
{
    private static SerialService NewService(FakeMachineRepository repo, List<string>? opened = null)
    {
        var s = new SerialService(repo, new AppOptions { DefaultSerialBootDelay = TimeSpan.Zero }, new AppReadiness(), NullLoggerFactory.Instance)
        {
            PortFactory = c =>
            {
                lock (opened ?? [])
                    opened?.Add(c.Path);
                return new FakeSerialPort();
            },
        };
        return s;
    }

    [Fact]
    public async Task Opens_only_com_prefixed_machines_once_per_port()
    {
        var repo = new FakeMachineRepository(
            new MasterMachine { MachineIP = "192.168.1.10", ProbeNo = 1 },
            new MasterMachine { MachineIP = "COM3", ProbeNo = 1 },
            new MasterMachine { MachineIP = "COM3", ProbeNo = 2, SType = "h" },
            new MasterMachine { MachineIP = "com5", ProbeNo = 1 });
        var opened = new List<string>();
        var s = NewService(repo, opened);

        await s.OpenConfiguredPortsAsync(CancellationToken.None);

        Assert.Null(s.GetConnection("192.168.1.10"));
        Assert.True(s.GetConnection("COM3")!.IsConnected);
        Assert.True(s.GetConnection("com5")!.IsConnected);
        Assert.Equal(["COM3", "com5"], opened.Order());
        await s.StopAsync(CancellationToken.None);
    }

    [Fact]
    public async Task No_machines_is_not_an_error()
    {
        var s = NewService(new FakeMachineRepository());
        await s.OpenConfiguredPortsAsync(CancellationToken.None);
        Assert.Null(s.GetConnection("COM3"));
    }

    [Fact]
    public async Task Db_error_is_surfaced()
    {
        var s = NewService(new FakeMachineRepository { GetAllError = new InvalidOperationException("simulated DB failure") });
        await Assert.ThrowsAsync<InvalidOperationException>(() => s.OpenConfiguredPortsAsync(CancellationToken.None));
    }

    [Theory]
    [InlineData("COM3", true)]
    [InlineData("com12", true)]
    [InlineData("192.168.1.1", false)]
    public void Serial_path_detection(string ip, bool expected) => Assert.Equal(expected, SerialService.IsSerialPath(ip));

    private static ProbeRequester Requester(SerialService serial, int tcpPort = 1) =>
        new(new TcpProbeClient(NullLogger<TcpProbeClient>.Instance), serial, new AppOptions { DefaultTcpPort = tcpPort }, NullLogger<ProbeRequester>.Instance);

    [Fact]
    public async Task Request_for_untracked_com_port_returns_nothing()
    {
        var probes = await Requester(NewService(new FakeMachineRepository())).RequestProbesAsync("COM9", "A", TimeSpan.FromMilliseconds(100));
        Assert.Empty(probes);
    }

    [Fact]
    public async Task Request_for_tracked_but_unconnected_port_returns_nothing()
    {
        var s = NewService(new FakeMachineRepository());
        s.Track("COM3"); // registered, never opened

        var probes = await Requester(s).RequestProbesAsync("COM3", "A", TimeSpan.FromMilliseconds(100));

        Assert.Empty(probes);
    }

    [Fact]
    public async Task Request_routes_network_ips_to_tcp()
    {
        await using var server = new FakeProbeServer([0x41, 0x41, 0x5a, 0x00, 0x5a, 0x19, 0xa3, 0x5a, 0x0d]);
        var probes = await Requester(NewService(new FakeMachineRepository()), server.Port)
            .RequestProbesAsync("127.0.0.1", "A", TimeSpan.FromSeconds(2));
        Assert.Equal(25.63, Assert.Single(probes).TempValue);
    }
}
