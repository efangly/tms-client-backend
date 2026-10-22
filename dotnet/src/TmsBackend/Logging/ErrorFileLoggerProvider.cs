using System.Collections.Concurrent;
using System.Globalization;

namespace TmsBackend.Logging;

/// <summary>
/// Writes Error+ log entries to logs/error_YYYY-MM-DD.txt, one file per day,
/// deleting files older than 30 days on rotation. Same file names and line
/// shape as internal/utils/logger.go so existing log habits keep working.
/// </summary>
public sealed class ErrorFileLoggerProvider : ILoggerProvider
{
    private static readonly TimeSpan Retention = TimeSpan.FromDays(30);

    private readonly string _dir;
    private readonly Func<DateTime> _now;
    private readonly ConcurrentDictionary<string, ErrorFileLogger> _loggers = new();
    private readonly Lock _lock = new();
    private StreamWriter? _writer;
    private string _currentDate = "";

    public ErrorFileLoggerProvider(string dir, Func<DateTime>? now = null)
    {
        _dir = dir;
        _now = now ?? (() => DateTime.Now);
    }

    public ILogger CreateLogger(string categoryName) =>
        _loggers.GetOrAdd(categoryName, name => new ErrorFileLogger(this, name));

    internal void Write(string category, LogLevel level, string message, Exception? exception)
    {
        var now = _now();
        var tag = level == LogLevel.Critical ? "CRITICAL" : "ERROR";
        // Go: "[ERROR] 2026/02/02 10:30:45 logger.go:54: msg" — category stands in for file:line.
        var line = string.Create(CultureInfo.InvariantCulture, $"[{tag}] {now:yyyy/MM/dd HH:mm:ss} {ShortCategory(category)}: {message}");
        if (exception != null)
            line += Environment.NewLine + exception;

        lock (_lock)
        {
            try
            {
                EnsureFile(now);
                _writer!.WriteLine(line);
                _writer.Flush();
            }
            catch
            {
                // Never let the error log take the app down.
            }
        }
    }

    private void EnsureFile(DateTime now)
    {
        var today = now.ToString("yyyy-MM-dd", CultureInfo.InvariantCulture);
        if (_writer != null && _currentDate == today)
            return;

        _writer?.Dispose();
        _writer = null;

        Directory.CreateDirectory(_dir);
        // Only runs when rotating to a new day, so this stays cheap.
        CleanupOldLogs(now);

        var path = Path.Combine(_dir, $"error_{today}.txt");
        _writer = new StreamWriter(new FileStream(path, FileMode.Append, FileAccess.Write, FileShare.ReadWrite));
        _currentDate = today;
    }

    private void CleanupOldLogs(DateTime now)
    {
        var cutoff = now - Retention;
        foreach (var file in Directory.EnumerateFiles(_dir, "error_*.txt"))
        {
            try
            {
                if (File.GetLastWriteTime(file) < cutoff)
                    File.Delete(file);
            }
            catch
            {
                // Locked or already gone — try again on the next rotation.
            }
        }
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
            _writer?.Dispose();
            _writer = null;
            _currentDate = "";
        }
    }

    private sealed class ErrorFileLogger(ErrorFileLoggerProvider provider, string category) : ILogger
    {
        public IDisposable? BeginScope<TState>(TState state) where TState : notnull => null;

        public bool IsEnabled(LogLevel logLevel) => logLevel >= LogLevel.Error;

        public void Log<TState>(LogLevel logLevel, EventId eventId, TState state, Exception? exception, Func<TState, Exception?, string> formatter)
        {
            if (!IsEnabled(logLevel))
                return;
            provider.Write(category, logLevel, formatter(state, exception), exception);
        }
    }
}
