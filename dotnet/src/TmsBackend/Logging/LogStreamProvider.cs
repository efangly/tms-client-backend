using System.Threading.Channels;

namespace TmsBackend.Logging;

/// <summary>One log line as sent to GET /api/log-stream.</summary>
public sealed record LogEntry(long Id, DateTime Time, LogLevel Level, string Category, string Message, string? Exception);

/// <summary>
/// Keeps the most recent Information+ log entries in memory and fans new ones
/// out to log-stream subscribers. Ids increase by one per entry, so a client
/// can resume with Last-Event-ID and spot gaps. A slow subscriber loses its
/// oldest pending entries rather than blocking the code that logs.
/// </summary>
public sealed class LogStreamProvider : ILoggerProvider
{
    public const int DefaultCapacity = 500;
    public const int SubscriberBuffer = 256;

    private readonly Func<DateTime> _now;
    private readonly LogEntry?[] _ring;
    private readonly List<Channel<LogEntry>> _subscribers = [];
    private readonly Lock _lock = new();
    private long _lastId;

    public LogStreamProvider(int capacity = DefaultCapacity, Func<DateTime>? now = null)
    {
        _ring = new LogEntry?[capacity];
        _now = now ?? (() => DateTime.Now);
    }

    public int Capacity => _ring.Length;

    public ILogger CreateLogger(string categoryName) => new StreamLogger(this, ShortCategory(categoryName));

    internal void Add(LogLevel level, string category, string message, Exception? exception)
    {
        lock (_lock)
        {
            var entry = new LogEntry(++_lastId, _now(), level, category, message, exception?.ToString());
            _ring[(int)((entry.Id - 1) % _ring.Length)] = entry;
            foreach (var ch in _subscribers)
                ch.Writer.TryWrite(entry);
        }
    }

    /// <summary>Buffered entries with an id above <paramref name="afterId"/>, oldest first.</summary>
    public List<LogEntry> Recent(long afterId = 0)
    {
        lock (_lock)
            return RecentLocked(afterId);
    }

    /// <summary>
    /// Snapshot and subscription taken under one lock, so no entry is missed or
    /// sent twice between the history and the live feed.
    /// </summary>
    public (List<LogEntry> History, Channel<LogEntry> Live) Subscribe(long afterId = 0)
    {
        var ch = Channel.CreateBounded<LogEntry>(new BoundedChannelOptions(SubscriberBuffer)
        {
            FullMode = BoundedChannelFullMode.DropOldest,
            SingleReader = true,
        });
        lock (_lock)
        {
            _subscribers.Add(ch);
            return (RecentLocked(afterId), ch);
        }
    }

    public void Unsubscribe(Channel<LogEntry> ch)
    {
        lock (_lock)
        {
            if (_subscribers.Remove(ch))
                ch.Writer.TryComplete();
        }
    }

    internal int SubscriberCount
    {
        get { lock (_lock) return _subscribers.Count; }
    }

    private List<LogEntry> RecentLocked(long afterId)
    {
        var first = Math.Max(afterId + 1, _lastId - _ring.Length + 1);
        var list = new List<LogEntry>((int)Math.Max(0, _lastId - first + 1));
        for (var id = first; id <= _lastId; id++)
            list.Add(_ring[(int)((id - 1) % _ring.Length)]!);
        return list;
    }

    private static string ShortCategory(string category)
    {
        var i = category.LastIndexOf('.');
        return i >= 0 ? category[(i + 1)..] : category;
    }

    public void Dispose()
    {
        lock (_lock)
        {
            foreach (var ch in _subscribers)
                ch.Writer.TryComplete();
            _subscribers.Clear();
        }
    }

    private sealed class StreamLogger(LogStreamProvider provider, string category) : ILogger
    {
        public IDisposable? BeginScope<TState>(TState state) where TState : notnull => null;

        public bool IsEnabled(LogLevel logLevel) => logLevel >= LogLevel.Information && logLevel != LogLevel.None;

        public void Log<TState>(LogLevel logLevel, EventId eventId, TState state, Exception? exception, Func<TState, Exception?, string> formatter)
        {
            if (!IsEnabled(logLevel))
                return;
            provider.Add(logLevel, category, formatter(state, exception), exception);
        }
    }
}
