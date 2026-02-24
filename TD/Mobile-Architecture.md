---
title: Mobile Architecture
id: "6914234"
space: TD
version: 2
labels:
    - architecture
    - mobile
    - ios
    - android
author: Robert Gonek
created_at: "2026-02-24T14:55:25Z"
last_modified_at: "2026-02-24T14:55:27Z"
last_modified_by: Robert Gonek
---
# Mobile Architecture

Luminary ships native SDKs for iOS and Android that are embedded in customers' apps. Internally, we also maintain a Luminary Companion app used by customer success and sales teams for demo and account monitoring — this document covers both the SDK architecture and the Companion app.

Current SDK versions shipping in the Companion app: **iOS SDK v3.2.1**, **Android SDK v3.2.0**.

## iOS

### Technology Stack

| Layer | Technology |
| --- | --- |
| UI | SwiftUI |
| Async/reactive | Combine + Swift Concurrency (`async/await`) |
| Persistence | SQLite via `GRDB.swift` |
| Networking | `URLSession` (async/await) |
| Architecture | MVVM |

### Application Structure

```
LuminaryCompanion-iOS/
├── App/
│   ├── LuminaryApp.swift         # @main entry, DI container setup
│   └── AppDelegate.swift         # SDK init, push notification registration
├── Features/
│   ├── Dashboard/                # Analytics overview screens
│   ├── Workspaces/               # Workspace switcher
│   └── Settings/                 # App settings, account info
├── Core/
│   ├── Networking/               # URLSession client, token refresh interceptor
│   ├── Persistence/              # GRDB database manager, migrations
│   └── Auth/                     # Keychain-backed token storage
└── SDK/                          # LuminarySDK embedded as Swift Package
```

### MVVM Pattern

ViewModels are `ObservableObject` classes that own business logic and expose `@Published` properties to views. Views are kept thin — no business logic, no direct API calls.

```swift
@MainActor
final class DashboardViewModel: ObservableObject {
    @Published var workspaces: [Workspace] = []
    @Published var isLoading = false
    @Published var error: AppError?

    private let workspaceService: WorkspaceServiceProtocol

    init(workspaceService: WorkspaceServiceProtocol = WorkspaceService()) {
        self.workspaceService = workspaceService
    }

    func loadWorkspaces() async {
        isLoading = true
        defer { isLoading = false }
        do {
            workspaces = try await workspaceService.fetchAll()
        } catch {
            self.error = AppError(underlying: error)
        }
    }
}
```

### Networking Layer

All HTTP requests go through a shared `APIClient` that handles:

- Attaching bearer tokens from Keychain
- Transparent token refresh on `401` using `async/await` with a `Task` lock to prevent concurrent refresh races
- Request/response logging in debug builds
- Certificate pinning (see below)

```swift
// Core/Networking/APIClient.swift
func request<T: Decodable>(_ endpoint: Endpoint) async throws -> T {
    var urlRequest = endpoint.urlRequest(baseURL: configuration.baseURL)
    urlRequest.setValue("Bearer \(try tokenStore.accessToken())", forHTTPHeaderField: "Authorization")
    let (data, response) = try await session.data(for: urlRequest)
    guard let httpResponse = response as? HTTPURLResponse else { throw NetworkError.invalidResponse }
    if httpResponse.statusCode == 401 {
        try await refreshTokenIfNeeded()
        return try await request(endpoint) // retry once
    }
    return try JSONDecoder.luminary.decode(T.self, from: data)
}
```

### Local SQLite Event Queue

The iOS SDK uses a SQLite database (managed by GRDB) as a durable event queue. Events are written synchronously to the queue on any thread, then flushed to the ingestion API by a background serial queue.

Schema:

```sql
CREATE TABLE pending_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    event_json  TEXT NOT NULL,
    created_at  INTEGER NOT NULL,  -- Unix timestamp
    attempts    INTEGER NOT NULL DEFAULT 0
);
```

Events are deleted from the queue only after a successful HTTP 200 response from the ingestion endpoint. If the app is killed mid-flush, events are retried on the next launch.

## Android

### Technology Stack

| Layer | Technology |
| --- | --- |
| UI | Jetpack Compose |
| Async | Kotlin Coroutines + Flow |
| DI | Hilt |
| Persistence | Room (SQLite) |
| Networking | Ktor Client |
| Architecture | MVVM + UDF |

### Application Structure

```
LuminaryCompanion-Android/
├── app/
│   ├── LuminaryApplication.kt    # Hilt entrypoint, SDK init
│   └── MainActivity.kt           # Single activity, NavHost
├── feature/
│   ├── dashboard/
│   ├── workspaces/
│   └── settings/
├── core/
│   ├── network/                  # Ktor client, interceptors
│   ├── database/                 # Room DAOs, entities, migrations
│   └── auth/                     # EncryptedSharedPreferences token store
└── sdk/                          # LuminarySDK AAR dependency
```

### MVVM with Unidirectional Data Flow

ViewModels expose `StateFlow<UiState>` and accept `UiEvent` via sealed classes, following the UDF pattern recommended by the Android team's architecture guidance.

```kotlin
data class DashboardUiState(
    val workspaces: List<Workspace> = emptyList(),
    val isLoading: Boolean = false,
    val error: String? = null,
)

@HiltViewModel
class DashboardViewModel @Inject constructor(
    private val workspaceRepository: WorkspaceRepository,
) : ViewModel() {

    private val _uiState = MutableStateFlow(DashboardUiState())
    val uiState: StateFlow<DashboardUiState> = _uiState.asStateFlow()

    fun loadWorkspaces() {
        viewModelScope.launch {
            _uiState.update { it.copy(isLoading = true) }
            workspaceRepository.getWorkspaces()
                .onSuccess { workspaces ->
                    _uiState.update { it.copy(workspaces = workspaces, isLoading = false) }
                }
                .onFailure { error ->
                    _uiState.update { it.copy(error = error.message, isLoading = false) }
                }
        }
    }
}
```

## Shared Concerns

### Certificate Pinning

Both iOS and Android pin the TLS certificate for `api.luminary.io` and `ingest.luminary.io`.

**iOS**: Uses `URLSessionDelegate` to validate the server's certificate against pinned public key hashes stored in `LuminarySDK.bundle`. Two hashes are pinned (current + backup) to allow certificate rotation without an app update.

**Android**: Uses OkHttp's `CertificatePinner`:

```kotlin
val pinner = CertificatePinner.Builder()
    .add("api.luminary.io", "sha256/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
    .add("api.luminary.io", "sha256/BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=") // backup
    .build()
```

**Certificate rotation procedure**: At least 30 days before the current certificate expires, add the new certificate's hash as the backup pin, release the SDK update, wait for adoption >90%, then remove the old hash. The rotation runbook lives in `operations/`.

### Biometric Authentication

Both apps support Face ID / Touch ID (iOS) and Fingerprint / Face Unlock (Android) for app unlock after backgrounding. Biometric auth does not replace the primary login — it unlocks a locally cached session. If the device biometrics are compromised or the cached session expires, users are prompted for full login.

The biometric prompt is triggered after 5 minutes of inactivity in the background.

### Background App Refresh / Event Flushing

**iOS**: The SDK registers a BGProcessingTask with identifier `io.luminary.sdk.flush`. This is scheduled when the app goes to background if the event queue is non-empty. The task flushes all queued events and calls `taskRequest.setTaskCompleted`.

**Android**: The SDK schedules a WorkManager `OneTimeWorkRequest` with network connectivity constraints when the event queue is non-empty. This ensures events are flushed even if the app was killed.

## Related

- [iOS Swift SDK](https://placeholder.invalid/page/sdk%2Fios-swift-sdk.md) — SDK integration documentation for customers
- [Android Kotlin SDK](https://placeholder.invalid/page/sdk%2Fandroid-kotlin-sdk.md) — SDK integration documentation for customers
- [Frontend Architecture](https://rgonek.atlassian.net/wiki/pages/viewpage.action?pageId=4882794) — Web application architecture
- [Ingestion Pipeline](https://placeholder.invalid/page/services) — Backend that receives SDK events
