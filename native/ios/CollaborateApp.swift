import SwiftUI
import PhotosUI
import UniformTypeIdentifiers

@main
struct CollaborateApp: App {
    var body: some Scene { WindowGroup { ChatView() } }
}

struct ChatView: View {
    @Environment(\.accessibilityReduceTransparency) private var reduceTransparency
    @State private var appearance = "System"
    @State private var draft = ""
    @State private var messages: [String] = []
    @State private var photos: [PhotosPickerItem] = []
    @State private var fileNames: [String] = []
    @State private var importing = false
    @State private var showPlugins = false
    @State private var members = ["Claude", "Codex", "Gemini"]
    @State private var photoCount = 0
    private let catalog = ["Claude", "Codex", "Gemini", "OpenCode", "Goose", "Aider", "Cline", "DeepSeek", "Qwen Code"]
    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 12) {
                    Text("Native interface preview • local only").font(.caption).foregroundStyle(.secondary)
                    ForEach(members, id: \.self) { name in
                        Label(name, systemImage: "person.crop.circle.fill").font(.subheadline)
                    }
                    ForEach(Array(messages.enumerated()), id: \.offset) { _, message in
                        HStack { Spacer(); Text(message).padding(12).foregroundStyle(.white).background(Color.blue, in: RoundedRectangle(cornerRadius: 20)) }
                    }
                }.padding()
            }
            .navigationTitle("Collaborate Asciidoc")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Menu { Picker("Appearance", selection: $appearance) { ForEach(["System", "Light", "Dark"], id: \.self) { Text($0) } } } label: { Image(systemName: "circle.lefthalf.filled") }.accessibilityLabel("Appearance")
                }
            }
            .safeAreaInset(edge: .bottom) {
                VStack(alignment: .leading, spacing: 8) {
                    if photoCount > 0 || !fileNames.isEmpty {
                        Text("\(photoCount) photos • \(fileNames.count) files selected (metadata preview only)").font(.caption)
                        Button("Clear attachments") { photos=[];photoCount=0;fileNames=[] }
                    }
                    if #available(iOS 26.0, *), !reduceTransparency {
                        GlassEffectContainer(spacing: 12) { composer.glassEffect(.regular, in: .rect(cornerRadius: 28)) }
                    } else { composer.background(.regularMaterial, in: RoundedRectangle(cornerRadius: 28)) }
                }.padding(12)
            }
            .fileImporter(isPresented: $importing, allowedContentTypes: [.item], allowsMultipleSelection: true) { result in
                if case let .success(urls) = result { fileNames = Array(urls.prefix(20)).map(\.lastPathComponent) }
            }
            .onChange(of: photos) { _, new in photoCount = new.count }
            .sheet(isPresented: $showPlugins) {
                NavigationStack {
                    List(catalog, id: \.self) { name in
                        Button { if !members.contains(name) { members.append(name) } } label: {
                            HStack { Text(name); Spacer(); Image(systemName: members.contains(name) ? "checkmark.circle" : "plus.circle") }
                        }.disabled(members.contains(name))
                    }.navigationTitle("AI personas").toolbar { Button("Done") { showPlugins=false } }
                }
            }
        }.preferredColorScheme(appearance == "Dark" ? .dark : appearance == "Light" ? .light : nil)
    }
    private var composer: some View {
        HStack(spacing: 10) {
            Menu {
                PhotosPicker(selection: $photos, maxSelectionCount: 20, matching: .images) { Label("Photos", systemImage: "photo.on.rectangle") }
                Button { importing=true } label: { Label("Files", systemImage: "folder") }
                Button { showPlugins=true } label: { Label("AI personas", systemImage: "person.badge.plus") }
            } label: { Image(systemName: "plus").frame(width:44,height:44) }.accessibilityLabel("Add attachments or AI personas")
            TextField("Message", text: $draft, axis: .vertical).lineLimit(1...4)
            Button {
                let text=draft.trimmingCharacters(in: .whitespacesAndNewlines)
                guard !text.isEmpty else { return }
                messages.append(text);draft=""
            } label: { Image(systemName:"arrow.up.circle.fill").font(.title2).frame(width:44,height:44) }.accessibilityLabel("Send local message").disabled(draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
        }.padding(5)
    }
}
