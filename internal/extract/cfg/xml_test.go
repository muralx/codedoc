package cfg

import "testing"

func TestXML(t *testing.T) {
	runCases(t, XML, []extractCase{
		{name: "maven pom", src: `<?xml version="1.0"?>
<project xmlns="http://maven.apache.org/POM/4.0.0" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:schemaLocation="x">
  <properties>
    <lib.mode>dev</lib.mode>
    <home>${JAVALIB_HOME}</home>
  </properties>
  <build>
    <plugin id="p1" dir="${OUT}">
      <name>
        compiler
      </name>
    </plugin>
  </build>
</project>
`,
			defs: []KeyDef{
				{"properties.lib.mode", 4, "dev"}, {"lib.mode", 4, "dev"},
				{"properties.home", 5, "${JAVALIB_HOME}"}, {"home", 5, "${JAVALIB_HOME}"},
				{"build.plugin@id", 8, "p1"}, {"build.plugin@dir", 8, "${OUT}"},
				{"build.plugin.name", 9, "compiler"},
			},
			refs: []KeyRef{{"JAVALIB_HOME", 5}, {"OUT", 8}}},
		{name: "spring beans, not maven", src: "<beans>\n<properties><a>1</a></properties>\n<bean id=\"ds\"><password>pw</password></bean>\n</beans>",
			defs: []KeyDef{{"properties.a", 2, "1"}, {"bean@id", 3, "ds"}, {"bean.password", 3, "***"}}},
		{name: "root text ignored", src: "<root>hello</root>"},
		{name: "mismatched tags", src: "<r>\n<a>1</a>\n<b></c></r>", defs: []KeyDef{{"a", 2, "1"}}, wantErr: true},
	})
}
